"""Command line parsing and the commands themselves, against a fake device."""

import argparse

import pytest
from fake_hid import FakeHandle

from hidpin import cli, protocol
from hidpin.device import Device
from hidpin.protocol import PinMode, PinSetting, Reason


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


def test_watch_prints_events_then_stops(fake_device, capsys, monkeypatch):
    device, handle = fake_device
    levels = handle.config.monitored_mask & ~(1 << 5)
    handle.queue_status(
        handle.current_status(
            seq=1,
            reason=Reason.LEVEL_CHANGED,
            levels=levels,
            events=(protocol.EdgeEvent(gpio=5, level=False, age_us=20_000),),
        )
    )

    def stop_after_queue(*args, **kwargs):
        if not handle.reads:
            raise KeyboardInterrupt
        return Device.read_status(device, *args, **kwargs)

    monkeypatch.setattr(device, "read_status", stop_after_queue)
    assert cli.main(["watch"]) == 0
    out = capsys.readouterr().out
    assert "GPIO5=OFF" in out.splitlines()[0]  # pull-up, still HIGH at the start
    assert "GPIO5  ON  (LOW)" in out
