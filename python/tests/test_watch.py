"""The Watch layer against simulated boards (the same cases as hidpin/watch_test.go)."""

import threading
import time
from datetime import datetime

import pytest

from hidpin import (
    ConnectFailed,
    HidpinError,
    Connected,
    DeviceDisconnected,
    Disconnected,
    InitialOnOff,
    OnOffChange,
    OutputResetCause,
    OutputsReset,
    PinConfig,
    PinConfigChanged,
    PinConfigRejected,
    PinConfigRestored,
    PinMode,
    PinSetting,
    ProtocolVersionError,
    Reason,
    SeveralDevices,
    StatusReceived,
    WatcherClosed,
    watch,
)
from hidpin import watcher as watcher_module
from hidpin.testing import PICO_AVAILABLE, DeviceGone, FakeBoard, FakeBus

SERIAL_A = "ABCD0123456789EF"
SERIAL_B = "FEDC9876543210AB"
TIMEOUT = 3.0


@pytest.fixture
def watchers():
    started = []
    yield started
    for watcher in started:
        watcher.close()


@pytest.fixture
def start(watchers):
    def start_watch(**options):
        options.setdefault("scan_interval", 0.005)
        watcher = watch(**options)
        watchers.append(watcher)
        return watcher

    return start_watch


def pins(settings) -> PinConfig:
    return PinConfig.unused().with_pins(settings)


def next_event(watcher):
    event = watcher.get(timeout=TIMEOUT)
    if event is None:
        pytest.fail("no event arrived")
    return event


def await_event(watcher, kind):
    """Skips events until one of the given kind arrives."""
    while True:
        event = next_event(watcher)
        if isinstance(event, kind):
            return event


def expect_no(watcher, kind, seconds):
    deadline = time.monotonic() + seconds
    while (remaining := deadline - time.monotonic()) > 0:
        event = watcher.get(timeout=remaining)
        if isinstance(event, kind):
            pytest.fail(f"unexpected {event}")


def await_stop(watcher):
    deadline = time.monotonic() + TIMEOUT
    while time.monotonic() < deadline:
        try:
            watcher.get(timeout=0.1)
        except WatcherClosed:
            return watcher.error
    pytest.fail("the watcher did not stop")


OWNER_TARGET = {
    5: PinSetting.monitor(PinMode.PULLUP, 20),
    6: PinSetting.monitor(PinMode.PULLDOWN, 0),
    7: PinSetting.output(False),
}


@pytest.fixture
def owner(start):
    """A watcher with a target on a plugged-in board that knows the ON/OFF of its monitored pins:
    GPIO5 OFF (pull-up, HIGH) and GPIO6 ON (pull-down, HIGH)."""
    board = FakeBoard(SERIAL_A)
    bus = FakeBus(board)
    target = pins(OWNER_TARGET)
    watcher = start(target_pins=target, bus=bus)
    initial = await_event(watcher, InitialOnOff)
    assert initial.on == {5: False, 6: True}
    return watcher, bus, board, target


def test_writes_the_target_and_learns_on_off(start):
    board = FakeBoard(SERIAL_A)
    target = pins(OWNER_TARGET)
    watcher = start(target_pins=target, bus=FakeBus(board))

    connected = await_event(watcher, Connected)
    assert connected.serial == SERIAL_A and not connected.reconnected
    assert connected.pin_config == target and board.pin_config == target
    reset = next_event(watcher)
    assert reset == OutputsReset([7], OutputResetCause.PIN_CONFIG)
    status = await_event(watcher, StatusReceived)
    assert status.status.reason & Reason.CONFIG_CHANGED
    assert status.status.monitored == 1 << 5 | 1 << 6
    assert next_event(watcher) == InitialOnOff({5: False, 6: True})


def test_reports_timed_changes(owner):
    watcher, _, board, _ = owner
    board.set_input(5, False)
    status = await_event(watcher, StatusReceived)
    change = next_event(watcher)
    assert isinstance(change, OnOffChange)
    assert (change.gpio, change.on, change.level, change.inferred, change.has_time) == (5, True, False, False, True)
    assert change.device_us == status.status.timestamp_us - 20_000
    age = (datetime.now().astimezone() - change.time).total_seconds()
    assert 0 <= age < 5
    assert status.on[5] and status.on[6]


def test_reconnects_after_unplugging(owner):
    watcher, bus, board, target = owner
    board.set_input(5, False)
    assert await_event(watcher, OnOffChange).on

    bus.unplug(board)
    assert isinstance(await_event(watcher, Disconnected).error, DeviceGone)
    with pytest.raises(DeviceDisconnected):
        watcher.set_outputs({7: True})
    board.set_input(5, True)  # GPIO5 back to OFF
    board.set_input(6, False)  # GPIO6 to OFF
    bus.plug(board)  # the board starts again with the default pin configuration

    connected = await_event(watcher, Connected)
    assert connected.reconnected and board.pin_config == target
    assert next_event(watcher) == OutputsReset([7], OutputResetCause.RECONNECT)
    changes = {}
    while len(changes) < 2:
        event = next_event(watcher)
        assert not isinstance(event, InitialOnOff), "pins known before were learned again"
        if isinstance(event, OnOffChange):
            changes[event.gpio] = event
    for gpio in (5, 6):
        assert not changes[gpio].on and changes[gpio].inferred and not changes[gpio].has_time


def test_stays_with_the_first_board(start):
    a, b = FakeBoard(SERIAL_A), FakeBoard(SERIAL_B)
    bus = FakeBus(a)
    watcher = start(bus=bus)
    assert await_event(watcher, Connected).serial == SERIAL_A
    bus.unplug(a)
    await_event(watcher, Disconnected)
    bus.plug(b)
    expect_no(watcher, Connected, 0.1)
    assert b.open_count == 0
    bus.plug(a)
    connected = await_event(watcher, Connected)
    assert connected.serial == SERIAL_A and connected.reconnected


def test_stops_when_it_cannot_choose_a_board(start):
    watcher = start(bus=FakeBus(FakeBoard(SERIAL_A), FakeBoard(SERIAL_B)))
    assert isinstance(await_stop(watcher), SeveralDevices)
    with pytest.raises(WatcherClosed):
        watcher.set_outputs({7: True})


def test_stops_when_the_target_does_not_suit_the_board(start):
    watcher = start(target_pins=pins({23: PinSetting.monitor()}), bus=FakeBus(FakeBoard(SERIAL_A)))
    error = await_stop(watcher)
    assert isinstance(error, PinConfigRejected) and error.gpio == 23


def test_rejects_bad_options():
    bus = FakeBus()
    bad = PinConfig.unused().with_pin(3, PinSetting(PinMode.UNUSED, 1))
    with pytest.raises(PinConfigRejected):
        watch(target_pins=bad, bus=bus)
    with pytest.raises(ValueError):
        watch(active_low={30: True}, bus=bus)
    with pytest.raises(ValueError):
        watch(scan_interval=-1, bus=bus)


def test_writes_the_target_back(owner):
    watcher, _, board, target = owner
    other = target.with_pins({5: PinSetting.unused(), 7: PinSetting.unused()})
    assert board.write_pin_config(other) == 0
    restored = await_event(watcher, PinConfigRestored)
    assert restored.found == other and restored.count == 1 and board.pin_config == target
    assert next_event(watcher) == OutputsReset([7], OutputResetCause.PIN_CONFIG)
    # GPIO5 was not monitored for a moment, so its ON/OFF is learned again.
    assert await_event(watcher, InitialOnOff).on == {5: False}


def test_without_target_follows_the_device(start):
    board = FakeBoard(SERIAL_A)
    watcher = start(bus=FakeBus(board))
    defaults = PinConfig.default(PICO_AVAILABLE)
    assert await_event(watcher, Connected).pin_config == defaults
    initial = await_event(watcher, InitialOnOff)
    assert len(initial.on) == 26 and not initial.on[5]
    other = defaults.with_pin(5, PinSetting.unused())
    board.write_pin_config(other)
    assert await_event(watcher, PinConfigChanged).config == other
    expect_no(watcher, PinConfigRestored, 0.05)
    assert board.pin_config == other


def test_infers_lost_changes(owner):
    watcher, _, board, _ = owner

    board.skip_reports(2)
    board.periodic()
    assert await_event(watcher, StatusReceived).missed == 2

    # The device dropped the edge event: only the level tells.
    board.lose_input(5, False)
    board.periodic()
    change = await_event(watcher, OnOffChange)
    assert (change.gpio, change.on, change.inferred, change.has_time) == (5, True, True, False)

    # GPIO5 went OFF without an edge event and then ON with one: the OFF is inferred from the edge
    # event, which must have left the other level.
    board.lose_input(5, True)
    board.set_input(5, False)
    first = await_event(watcher, OnOffChange)
    second = next_event(watcher)
    assert not first.on and first.inferred
    assert second.on and not second.inferred and second.has_time


def test_polarity_override(start):
    watcher = start(
        target_pins=pins({5: PinSetting.monitor(PinMode.PULLUP, 20)}),
        active_low={5: False},
        bus=FakeBus(FakeBoard(SERIAL_A)),
    )
    assert await_event(watcher, InitialOnOff).on[5], "GPIO5 HIGH should be ON"


def test_reports_output_resets(owner):
    watcher, _, board, target = owner
    watcher.set_outputs({7: True})
    assert board.level(7)

    board.suspend()
    assert await_event(watcher, OutputsReset) == OutputsReset([7], OutputResetCause.SUSPEND)
    assert not board.level(7), "the output was driven back"

    board.reenumerate()
    await_event(watcher, Disconnected)
    connected = await_event(watcher, Connected)
    assert connected.reconnected and connected.pin_config == target
    assert next_event(watcher).cause == OutputResetCause.RECONNECT


def test_set_target_pins(owner):
    watcher, bus, board, target = owner

    changed = target.with_pin(8, PinSetting.output(True))
    watcher.set_target_pins(changed)
    assert board.pin_config == changed
    reset = await_event(watcher, OutputsReset)
    assert reset.gpios == [8] and board.level(8)

    unsuitable = changed.with_pin(23, PinSetting.monitor())
    with pytest.raises(PinConfigRejected):
        watcher.set_target_pins(unsuitable)
    assert board.pin_config == changed

    bus.unplug(board)
    await_event(watcher, Disconnected)
    later = target.with_pin(5, PinSetting.monitor(PinMode.PULLDOWN, 5))
    watcher.set_target_pins(later)
    with pytest.raises(PinConfigRejected):
        watcher.set_target_pins(unsuitable)
    bus.plug(board)
    assert await_event(watcher, Connected).pin_config == later
    assert board.pin_config == later


def test_reports_connect_failures_once(start):
    board = FakeBoard(SERIAL_A)
    board.set_open_error(PermissionError("permission denied"))
    watcher = start(bus=FakeBus(board))
    assert isinstance(await_event(watcher, ConnectFailed).error, PermissionError)
    expect_no(watcher, ConnectFailed, 0.05)

    board.set_open_error(None)
    board.set_protocol_version(2)
    assert isinstance(await_event(watcher, ConnectFailed).error, ProtocolVersionError)
    board.set_protocol_version(1)
    await_event(watcher, Connected)


def test_only_reports_failures_that_last(start):
    board = FakeBoard(SERIAL_A)
    board.set_open_error(PermissionError("permission denied"))
    watcher = start(bus=FakeBus(board), scan_interval=0.2)
    time.sleep(0.05)  # one failed try: udev may not have set the permissions yet
    board.set_open_error(None)
    assert isinstance(next_event(watcher), Connected)


def test_methods_can_be_called_while_events_wait(start):
    board = FakeBoard(SERIAL_A)
    watcher = start(target_pins=pins({7: PinSetting.output(False)}), bus=FakeBus(board))
    await_event(watcher, Connected)
    # More events are waiting to be taken; the watcher must still serve this call.
    errors = []
    caller = threading.Thread(target=lambda: errors.append(watcher.set_outputs({7: True})))
    caller.start()
    caller.join(TIMEOUT)
    assert not caller.is_alive(), "set_outputs blocked while events were waiting"
    assert errors == [None] and board.level(7)


def test_keeps_reading_only_as_fast_as_events_are_taken(start):
    board = FakeBoard(SERIAL_A)
    watcher = start(bus=FakeBus(board))
    await_event(watcher, InitialOnOff)
    for i in range(200):
        board.set_input(5, i % 2 == 1)
    time.sleep(0.2)
    # The application took nothing meanwhile: the watcher stopped reading, and the board kept only
    # as many reports as a hidraw queue holds. The next report shows the gap.
    board.periodic()
    missed = 0
    while (event := watcher.get(timeout=0.2)) is not None:
        if isinstance(event, StatusReceived):
            missed += event.missed
    assert missed > 0


def test_close_without_taking_events(start):
    board = FakeBoard(SERIAL_A)
    watcher = start(bus=FakeBus(board))
    deadline = time.monotonic() + TIMEOUT
    while board.open_count == 0 and time.monotonic() < deadline:
        time.sleep(0.001)
    for i in range(10):
        board.set_input(5, i % 2 == 1)
    closer = threading.Thread(target=watcher.close)
    closer.start()
    closer.join(TIMEOUT)
    assert not closer.is_alive(), "close blocked"
    assert await_stop(watcher) is None and board.open_count == 0
    with pytest.raises(WatcherClosed):
        watcher.set_target_pins(PinConfig.unused())


def test_iterating_ends_when_the_watcher_stops(start):
    watcher = start(bus=FakeBus(FakeBoard(SERIAL_A), FakeBoard(SERIAL_B)))
    assert list(watcher) == []
    assert isinstance(watcher.error, SeveralDevices)


# --- parts of the watcher on their own --------------------------------------------------


def test_device_clock_uses_the_least_delayed_sample():
    clock = watcher_module._DeviceClock()
    clock.add(100.005, 1_000_000)  # read 5 ms late
    clock.add(101.001, 2_000_000)  # read 1 ms late
    clock.add(102.030, 3_000_000)  # read 30 ms late
    assert clock.monotonic_at(2_500_000) == pytest.approx(101.501, abs=1e-9)


def test_device_clock_forgets_old_samples():
    window = watcher_module._CLOCK_WINDOW
    clock = watcher_module._DeviceClock()
    clock.add(100.0, 0)  # a perfect sample, but the clocks drift apart afterwards
    for i in range(1, 4):
        clock.add(100.0 + i * window + 0.010, int(i * window * 1e6))
    assert clock.monotonic_at(int(3 * window * 1e6)) == pytest.approx(100.0 + 3 * window + 0.010, abs=1e-9)


def test_device_clock_gives_local_time():
    clock = watcher_module._DeviceClock()
    now = time.monotonic()
    clock.add(now, 5_000_000)
    estimate = clock.at(4_000_000)  # a second before the sample
    assert estimate.tzinfo is not None
    assert abs((datetime.now().astimezone() - estimate).total_seconds() - 1.0) < 0.5


def test_driven_outputs():
    prev = PinConfig.unused().with_pins(
        {1: PinSetting.output(False), 2: PinSetting.output(False), 3: PinSetting.monitor(), 4: PinSetting.output(True)}
    )
    new = PinConfig.unused().with_pins(
        {
            1: PinSetting.output(False),  # unchanged
            2: PinSetting.output(True),  # initial level changed
            3: PinSetting.output(False),  # became an output
            4: PinSetting.unused(),  # no longer an output
        }
    )
    assert watcher_module._driven_outputs(prev, new) == [2, 3]


def test_output_reset_causes_describe_themselves():
    assert [cause.description for cause in OutputResetCause] == [
        "USB suspend or bus reset",
        "reconnected",
        "pin configuration written",
    ]


# --- more of the watcher ------------------------------------------------------------------


def test_watch_checks_the_usb_ids_at_once(monkeypatch):
    monkeypatch.setenv("HIDPIN_PID", "0x10000")
    with pytest.raises(HidpinError, match="HIDPIN_PID"):
        watch()


def test_set_target_pins_validates_before_reaching_the_device(owner):
    watcher, _, board, target = owner
    with pytest.raises(PinConfigRejected) as rejected:
        watcher.set_target_pins(target.with_pin(3, PinSetting(PinMode.UNUSED, 1)))
    assert rejected.value.local and rejected.value.gpio == 3
    assert board.pin_config == target


def test_set_target_pins_to_the_current_one_writes_nothing(owner):
    watcher, _, board, target = owner
    watcher.set_target_pins(target)
    expect_no(watcher, OutputsReset, 0.1)
    assert board.pin_config == target


def test_bus_failures_are_reported_and_retried(start):
    class FlakyBus(FakeBus):
        failing = True

        def devices(self):
            if self.failing:
                raise OSError("cannot list /sys/class/hidraw")
            return super().devices()

    bus = FlakyBus(FakeBoard(SERIAL_A))
    watcher = start(bus=bus)
    assert str(await_event(watcher, ConnectFailed).error) == "cannot list /sys/class/hidraw"
    bus.failing = False
    await_event(watcher, Connected)


def test_writing_back_is_limited_to_once_a_second(owner):
    watcher, _, board, target = owner
    other = target.with_pin(5, PinSetting.unused())
    board.write_pin_config(other)
    first = await_event(watcher, PinConfigRestored)
    restored_at = time.monotonic()
    board.write_pin_config(other)
    second = await_event(watcher, PinConfigRestored)
    assert (first.count, second.count) == (1, 2)
    assert time.monotonic() - restored_at >= 0.9
    assert board.pin_config == target
