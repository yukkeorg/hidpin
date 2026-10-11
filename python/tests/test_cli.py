"""Command line parsing and the commands themselves, against a fake device."""

import argparse
import json
import threading
import time
from dataclasses import replace

import pytest
from fake_hid import FakeHandle

from hidpin import cli, protocol
from hidpin.testing import FakeBoard, FakeBus
from hidpin.device import Device, DeviceEntry
from hidpin.protocol import PinMode, PinSetting


def test_parse_pin_spec_forms():
    assert cli.parse_pin_spec("5=pullup:20") == (5, PinSetting(PinMode.PULLUP, 20))
    assert cli.parse_pin_spec("6=off") == (6, PinSetting(PinMode.UNUSED, 0))
    assert cli.parse_pin_spec("7=out:low") == (7, PinSetting(PinMode.OUTPUT, 0))
    assert cli.parse_pin_spec("8=out:high") == (8, PinSetting(PinMode.OUTPUT, 1))
    assert cli.parse_pin_spec("9=pulldown") == (9, PinSetting(PinMode.PULLDOWN, protocol.DEFAULT_DEBOUNCE_MS))
    assert cli.parse_pin_spec("10=nopull:0") == (10, PinSetting(PinMode.INPUT, 0))
    assert cli.parse_pin_spec("11=in:255") == (11, PinSetting(PinMode.INPUT, 255))


@pytest.mark.parametrize(
    "text",
    ["5", "5=", "x=off", "30=off", "5=bogus", "5=pullup:abc", "5=pullup:256", "5=off:1", "5=out:maybe"],
)
def test_parse_pin_spec_rejects(text):
    with pytest.raises(argparse.ArgumentTypeError):
        cli.parse_pin_spec(text)


def test_parse_output_spec():
    assert cli.parse_output_spec("7=high") == (7, True)
    assert cli.parse_output_spec("7=0") == (7, False)
    with pytest.raises(argparse.ArgumentTypeError):
        cli.parse_output_spec("7")
    with pytest.raises(argparse.ArgumentTypeError):
        cli.parse_output_spec("7=maybe")


@pytest.fixture
def fake_device(monkeypatch):
    handle = FakeHandle()
    device = Device(handle, serial="ABCD0123456789EF")
    monkeypatch.setattr(cli.Device, "open", classmethod(lambda cls, serial=None, **kwargs: device))
    return device, handle


@pytest.mark.parametrize("argv", [[], ["--json"], ["config"]])
def test_missing_command_prints_help_and_error(argv, capsys):
    with pytest.raises(SystemExit) as exc:
        cli.main(argv)
    assert exc.value.code == 2
    captured = capsys.readouterr()
    assert captured.out == ""
    assert captured.err.startswith("usage: hidpin")
    assert "show this help message and exit" in captured.err
    last = captured.err.strip().splitlines()[-1]
    assert last.startswith("hidpin") and "error: the following arguments are required" in last


def test_other_usage_errors_print_only_the_short_usage(capsys):
    with pytest.raises(SystemExit) as exc:
        cli.main(["bogus"])
    assert exc.value.code == 2
    assert "show this help message and exit" not in capsys.readouterr().err


def test_info_command(fake_device, capsys):
    assert cli.main(["info"]) == 0
    out = capsys.readouterr().out
    assert "Raspberry Pi Pico" in out
    assert "26 pins" in out


def as_rp2350_a2(handle: FakeHandle) -> None:
    handle.info = replace(
        handle.info,
        board=protocol.Board.PICO2,
        chip=protocol.Chip.RP2350A,
        chip_revision=2,
        quirks=protocol.Quirk.PULL_DOWN_UNRELIABLE,
    )


def test_info_shows_the_chip_and_known_problems(fake_device, capsys):
    as_rp2350_a2(fake_device[1])
    assert cli.main(["info"]) == 0
    out = capsys.readouterr().out
    assert "Raspberry Pi Pico 2" in out
    assert "RP2350A (revision 2)" in out
    assert "erratum E9" in out



def test_info_shows_a_board_without_a_preset(fake_device, capsys):
    import json

    handle = fake_device[1]
    handle.info = replace(handle.info, board=protocol.Board.OTHER, sdk_board="pico_w")
    assert cli.main(["info"]) == 0
    out = capsys.readouterr().out
    assert "board             : pico_w" in out
    assert "Pico SDK board    : pico_w" in out
    assert cli.main(["--json", "info"]) == 0
    payload = json.loads(capsys.readouterr().out)
    assert (payload["board"], payload["board_name"], payload["sdk_board"]) == (0, "pico_w", "pico_w")

def test_config_set_warns_about_pull_downs_on_rp2350_a2(fake_device, capsys):
    as_rp2350_a2(fake_device[1])
    assert cli.main(["config", "set", "5=pulldown:20"]) == 0
    captured = capsys.readouterr()
    assert captured.err.startswith("warning: GPIO5 uses the internal pull-down")
    assert "GPIO5 is now" in captured.out


def test_info_command_json(fake_device, capsys):
    import json

    assert cli.main(["--json", "info"]) == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload["board_name"] == "Raspberry Pi Pico"
    assert payload["firmware"] == "0.1.0"
    assert len(payload["available_gpios"]) == 26


def test_config_set_command(fake_device, capsys):
    device, handle = fake_device
    assert cli.main(["config", "set", "5=pulldown:5", "7=out:high"]) == 0
    assert handle.config[5] == PinSetting(PinMode.PULLDOWN, 5)
    assert handle.config[7] == PinSetting(PinMode.OUTPUT, 1)
    assert "GPIO5 is now pulldown:5" in capsys.readouterr().out


def test_config_set_rejects_unavailable_gpio(fake_device, capsys):
    assert cli.main(["config", "set", "23=pullup:20"]) == 1
    assert "GPIO23" in capsys.readouterr().err


def test_config_get_command(fake_device, capsys):
    assert cli.main(["config", "get"]) == 0
    out = capsys.readouterr().out
    assert "GPIO0  pullup:20" in out
    assert "GPIO23" not in out  # not an available GPIO on the Pico


def test_output_command_warns_about_non_output_pins(fake_device, capsys):
    device, handle = fake_device
    cli.main(["config", "set", "7=out:low"])
    capsys.readouterr()

    assert cli.main(["output", "7=high", "8=high"]) == 0
    captured = capsys.readouterr()
    assert "GPIO8 is not an output pin" in captured.err
    mask, value = protocol.decode_output(handle.output_writes[-1][1:])
    assert mask == (1 << 7) | (1 << 8)
    assert value == mask


class RunningWatch:
    """The watch command running on a thread against a simulated board, as a user would see it."""

    def __init__(self, capsys, argv):
        self._capsys = capsys
        self._stop = threading.Event()
        self.out = ""
        self.err = ""
        self.code = None
        cli.watch_stop = self._stop
        self._thread = threading.Thread(target=self._run, args=(argv,))
        self._thread.start()

    def _run(self, argv):
        self.code = cli.main(argv)

    def _collect(self):
        captured = self._capsys.readouterr()
        self.out += captured.out
        self.err += captured.err

    def wait_for(self, text, stream="out"):
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            self._collect()
            if text in getattr(self, stream):
                return
            time.sleep(0.005)
        pytest.fail(f"{text!r} never appeared in {getattr(self, stream)!r}")

    def stop(self):
        self._stop.set()
        self._thread.join(3)
        assert not self._thread.is_alive(), "the watch command did not stop"
        self._collect()
        return self.code


@pytest.fixture
def board(monkeypatch):
    board = FakeBoard("ABCD0123456789EF")
    bus = FakeBus(board)
    monkeypatch.setattr(cli, "watch_bus", bus)
    monkeypatch.setattr(cli, "scan_interval", 0.005)
    monkeypatch.setattr(cli, "watch_stop", None)
    return bus, board


def test_watch_prints_events_until_stopped(board, capsys):
    _, fake = board
    running = RunningWatch(capsys, ["watch"])
    running.wait_for("GPIO5=OFF")
    fake.set_input(5, False)
    running.wait_for("GPIO5  ON  (LOW)")
    assert running.stop() == 0
    lines = running.out.strip().splitlines()
    assert len(lines) == 2 and lines[0].startswith("GPIO0=OFF GPIO1=OFF")
    assert lines[1] == "[  4.981000] GPIO5  ON  (LOW)"


def test_watch_survives_unplugging(board, capsys):
    bus, fake = board
    running = RunningWatch(capsys, ["watch", "--active-high", "5"])
    running.wait_for("GPIO5=ON")
    bus.unplug(fake)
    running.wait_for("the device was disconnected; waiting for it to come back")
    fake.set_input(5, False)
    bus.plug(fake)
    running.wait_for("the device is connected again")
    running.wait_for("[         ?] GPIO5  OFF (LOW)")
    assert running.stop() == 0


def test_watch_json_keeps_stdout_for_reports(board, capsys):
    bus, fake = board
    running = RunningWatch(capsys, ["--json", "watch"])
    running.wait_for('"reason": ["HOST_REQUEST"]')
    fake.set_input(5, False)
    running.wait_for('"events": [{"gpio": 5, "level": false, "age_us": 20000, "start_us": 4981000}]')
    bus.unplug(fake)
    running.wait_for("the device was disconnected", stream="err")
    running.stop()
    for line in running.out.strip().splitlines():
        json.loads(line)
    assert '"on": {"0": false, "1": false' in running.out


def test_watch_fails_without_device(board, capsys):
    bus, fake = board
    bus.unplug(fake)
    assert cli.main(["watch"]) == 1
    assert capsys.readouterr().err == "error: no hidpin device found\n"


def test_watch_reports_configuration_changes_and_output_resets(board, capsys):
    _, fake = board
    running = RunningWatch(capsys, ["watch"])
    running.wait_for("GPIO5=OFF")
    fake.write_pin_config(fake.pin_config.with_pin(7, PinSetting.output(False)))  # another program
    running.wait_for("the pin configuration changed")
    fake.suspend()
    running.wait_for("outputs returned to their initial level (USB disconnect or suspend)")
    assert running.stop() == 0


def test_watch_fails_when_the_device_cannot_be_opened(board, capsys):
    _, fake = board
    fake.set_open_error(PermissionError("permission denied"))
    assert cli.main(["watch"]) == 1
    assert capsys.readouterr().err == "error: permission denied\n"


def test_watch_rejects_a_bad_gpio_list(board, capsys):
    assert cli.main(["watch", "--active-low", "5,x"]) == 1
    assert capsys.readouterr().err.startswith("error: bad GPIO list")


def test_list_command(monkeypatch, capsys):
    monkeypatch.setattr(cli, "find_devices", lambda: [DeviceEntry(b"/dev/hidraw0", "ABCD0123456789EF", "yukke.org", "hidpin")])
    assert cli.main(["list"]) == 0
    assert capsys.readouterr().out == "ABCD0123456789EF  yukke.org hidpin\n"
    assert cli.main(["--json", "list"]) == 0
    assert json.loads(capsys.readouterr().out) == [
        {"serial": "ABCD0123456789EF", "manufacturer": "yukke.org", "product": "hidpin"}
    ]
    monkeypatch.setattr(cli, "find_devices", lambda: [])
    assert cli.main(["list"]) == 0
    assert capsys.readouterr().out == "no hidpin device found\n"
