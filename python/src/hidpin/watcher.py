"""The Watch layer: keeps one device connected for a long-running program (docs/HOST_API.md 9).

watch() returns a Watcher, whose events are taken by iterating it from any thread. async_watch()
returns an AsyncWatcher with the same behaviour for asyncio. Both run the device on a thread of
their own.
"""

from __future__ import annotations

import asyncio
import concurrent.futures
import enum
import threading
import time
from collections import deque
from collections.abc import Callable, Iterator
from dataclasses import dataclass, field
from datetime import datetime, timezone
from types import TracebackType
from typing import Any

from hidpin import protocol
from hidpin.device import (
    Bus,
    Device,
    DeviceEntry,
    HidpinError,
    PinConfigConflict,
    PinConfigRejected,
    SeveralDevices,
    SystemBus,
    usb_ids,
)
from hidpin.protocol import DeviceInfo, PinConfig, Reason, Result, StatusReport

DEFAULT_SCAN_INTERVAL = 1.0  # seconds between looks for the device while it is disconnected

_ALL_GPIOS = (1 << protocol.GPIO_COUNT) - 1
_REPORT_LEN = 1 + protocol.REPORT_PAYLOAD_LEN
# How long one read of the device waits, which bounds how long a call waits for the watcher.
_READ_POLL = 0.05
# The shortest time between two writes of the target over a change made by another program.
_RESTORE_INTERVAL = 1.0
# How long to wait for CONFIG_CHANGED after writing the pin configuration before asking instead.
_SETTLE_TIMEOUT = 1.0
# How long one clock sample is trusted.
_CLOCK_WINDOW = 30.0
# Events waiting for the application; while they fill it, the watcher stops reading the device.
_OUT_CAPACITY = 16


class DeviceDisconnected(HidpinError):
    """An operation needs the device while it is disconnected."""

    def __init__(self) -> None:
        super().__init__("the hidpin device is not connected")


class WatcherClosed(HidpinError):
    """The watcher has stopped."""

    def __init__(self) -> None:
        super().__init__("the watcher has stopped")


# --- events ----------------------------------------------------------------------------


@dataclass(frozen=True)
class Connected:
    """The device was opened and, with a target pin configuration, made to follow it."""

    serial: str
    path: bytes
    info: DeviceInfo
    pin_config: PinConfig  # the pin configuration of the device from now on
    reconnected: bool  # False for the first connection only


@dataclass(frozen=True)
class Disconnected:
    """The connection was lost; the watcher keeps looking for the same device."""

    error: BaseException


@dataclass(frozen=True)
class ConnectFailed:
    """The device is there but cannot be used (permissions, protocol version, ...). Reported once
    per cause; the watcher keeps trying."""

    error: BaseException


@dataclass(frozen=True)
class StatusReceived:
    """A status report as received, including the ones the watcher asks for."""

    status: StatusReport
    missed: int  # status reports lost just before this one
    on: dict[int, bool] = field(default_factory=dict)  # ON/OFF of every monitored pin in it


@dataclass(frozen=True)
class InitialOnOff:
    """ON/OFF of monitored pins the watcher did not know before. It is not a change."""

    on: dict[int, bool] = field(default_factory=dict)


@dataclass(frozen=True)
class OnOffChange:
    """The ON/OFF of a monitored pin changed.

    time and device_us are the moment the change began, in host time and on the device clock
    (microseconds since it started); both are None when it is not known. inferred is True when no
    edge event reported the change: the watcher found it by comparing pin levels.
    """

    gpio: int
    on: bool
    level: bool
    time: datetime | None = None
    device_us: int | None = None
    inferred: bool = False

    @property
    def has_time(self) -> bool:
        return self.device_us is not None


class OutputResetCause(enum.IntEnum):
    """Why output pins went back to their initial output level."""

    SUSPEND = 1  # the device reported a USB suspend or bus reset (OUTPUT_RESET)
    RECONNECT = 2  # the device was connected again after it had been disconnected
    PIN_CONFIG = 3  # a pin configuration write made them outputs or changed their initial level

    @property
    def description(self) -> str:
        return {
            OutputResetCause.SUSPEND: "USB suspend or bus reset",
            OutputResetCause.RECONNECT: "reconnected",
            OutputResetCause.PIN_CONFIG: "pin configuration written",
        }[self]


@dataclass(frozen=True)
class OutputsReset:
    """These output pins are at their initial output level now. The watcher never drives them back
    to earlier levels; the application decides whether to."""

    gpios: list[int]
    cause: OutputResetCause


@dataclass(frozen=True)
class PinConfigChanged:
    """The pin configuration of the device changed. Only a watcher without a target reports it."""

    config: PinConfig


@dataclass(frozen=True)
class PinConfigRestored:
    """Another program changed the pin configuration and the watcher wrote the target back.

    found is what it found; count grows each time, and a count that keeps growing means a fight.
    """

    found: PinConfig
    count: int


Event = (
    Connected
    | Disconnected
    | ConnectFailed
    | StatusReceived
    | InitialOnOff
    | OnOffChange
    | OutputsReset
    | PinConfigChanged
    | PinConfigRestored
)


# --- the watcher --------------------------------------------------------------------


class _Fatal(Exception):
    """Wraps an error that stops the watcher."""

    def __init__(self, error: BaseException) -> None:
        super().__init__(str(error))
        self.error = error


class _DeviceClock:
    """Converts device timestamps to host time.

    A report reaches the host some time after the device stamped it, so the sample with the
    smallest host-minus-device difference is the most accurate. The best sample of the current and
    the previous window is used, so the estimate follows the drift between the two clocks. Host
    time here is time.monotonic().
    """

    def __init__(self) -> None:
        self._cur: tuple[float, int] | None = None
        self._prev: tuple[float, int] | None = None
        self._cur_start = 0.0

    @staticmethod
    def _closer(a: tuple[float, int], b: tuple[float, int]) -> bool:
        return (a[0] - b[0]) < (a[1] - b[1]) / 1e6

    def add(self, host: float, device_us: int) -> None:
        sample = (host, device_us)
        if self._cur is None or host - self._cur_start >= _CLOCK_WINDOW:
            self._prev, self._cur, self._cur_start = self._cur, sample, host
        elif self._closer(sample, self._cur):
            self._cur = sample

    def monotonic_at(self, device_us: int) -> float:
        """The host time (time.monotonic()) of a device time."""
        assert self._cur is not None
        best = self._cur
        if self._prev is not None and self._closer(self._prev, best):
            best = self._prev
        return best[0] + (device_us - best[1]) / 1e6

    def at(self, device_us: int) -> datetime:
        wall = time.time() - time.monotonic() + self.monotonic_at(device_us)
        return datetime.fromtimestamp(wall, tz=timezone.utc).astimezone()


class _Connection:
    """One open device, from opening it until it is lost or closed."""

    def __init__(self, device: Device, handle: Any) -> None:
        self.device = device
        self.handle = handle
        self.clock = _DeviceClock()
        self.last_seq: int | None = None
        # The seq of the status requested after connecting: interrupt reports up to it were sent
        # before that status, which already reflects them.
        self.snapshot_seq: int | None = None
        # Set from writing the pin configuration until the device reports CONFIG_CHANGED; reports
        # sent before that still show the old configuration (PROTOCOL.md 6.4).
        self.awaiting_config = False


class Watcher:
    """Keeps one device connected for a long-running program.

    It waits for the device, reopens it after it was unplugged, keeps its pin configuration at the
    target and reports what happens as events: iterate it, or call get(). Every method is safe to
    call from any thread, including the one that takes the events. While the application does not
    take events, the watcher stops reading the device; reports lost that way show up as
    StatusReceived.missed.
    """

    def __init__(
        self,
        *,
        bus: Bus,
        serial: str | None,
        target_pins: PinConfig | None,
        active_low: dict[int, bool],
        scan_interval: float,
    ) -> None:
        self._bus = bus
        self._serial = serial
        self._target = target_pins
        self._active_low = dict(active_low)
        self._interval = scan_interval

        self._cv = threading.Condition()
        self._out: deque[Event] = deque()  # events for the application
        self._requests: deque[tuple[Callable[[], Any], concurrent.futures.Future]] = deque()
        self._listeners: list[Callable[[], None]] = []
        self._stopping = False
        self._finished = False
        self._error: BaseException | None = None

        # State of the thread that owns the device; only it touches these.
        self._pending: deque[Event] = deque()
        self._info: DeviceInfo | None = None
        self._config = PinConfig.unused()  # the pin configuration of the device, as last seen
        self._known: dict[int, bool] = {}  # the ON/OFF of monitored pins, kept across connections
        self._ever_connected = False
        self._conn: _Connection | None = None
        self._failure = ""
        self._failures = 0
        self._restore_count = 0
        self._last_restore = float("-inf")
        self._scan_at: float | None = time.monotonic()
        self._restore_at: float | None = None
        self._settle_at: float | None = None

        self._thread = threading.Thread(target=self._run, name="hidpin-watch", daemon=True)
        self._thread.start()

    # --- the application side -----------------------------------------------------------

    def get(self, timeout: float | None = None) -> Event | None:
        """Takes the next event, waiting at most timeout seconds (None: as long as it takes).

        Returns None when the timeout expires, and raises WatcherClosed once the watcher has
        stopped and every event has been taken; error then tells why it stopped.
        """
        deadline = None if timeout is None else time.monotonic() + timeout
        with self._cv:
            while not self._out:
                if self._finished:
                    raise WatcherClosed()
                remaining = None if deadline is None else deadline - time.monotonic()
                if remaining is not None and remaining <= 0:
                    return None
                self._cv.wait(remaining)
            event = self._out.popleft()
            self._cv.notify_all()
            return event

    def __iter__(self) -> Iterator[Event]:
        while True:
            try:
                event = self.get()
            except WatcherClosed:
                return
            if event is not None:
                yield event

    @property
    def error(self) -> BaseException | None:
        """Why the watcher stopped: PinConfigRejected when the target does not suit the board,
        SeveralDevices, or None when it was closed (and while it runs)."""
        with self._cv:
            return self._error

    def close(self) -> None:
        """Stops the watcher and closes the device. Events not yet taken are dropped."""
        with self._cv:
            self._stopping = True
            self._cv.notify_all()
        if threading.current_thread() is not self._thread:
            self._thread.join()

    def __enter__(self) -> Watcher:
        return self

    def __exit__(self, exc_type: type[BaseException] | None, exc: BaseException | None, tb: TracebackType | None) -> None:
        self.close()

    def set_outputs(self, levels: dict[int, bool]) -> None:
        """Drives output pins like Device.set_outputs. Raises DeviceDisconnected while the device is
        disconnected; the levels are not remembered for later."""

        def run() -> None:
            if self._conn is None:
                raise DeviceDisconnected()
            self._conn.device.set_outputs(levels)

        self._call(run)

    def set_target_pins(self, config: PinConfig) -> None:
        """Replaces the target pin configuration; a watcher without one gets one.

        It is checked against the board once the watcher has met it, and one that does not suit
        the board raises PinConfigRejected, keeping the old target. While the device is connected
        it is written at once; if that fails for another reason the new target stays and is written
        again later. While the device is disconnected it is written on the next connection.
        """
        result, gpio = protocol.validate_pin_config(config, _ALL_GPIOS)
        if result != Result.OK:
            raise PinConfigRejected(result, gpio, local=True)
        self._call(lambda: self._set_target(config))

    def _call(self, run: Callable[[], Any]) -> Any:
        """Runs run on the thread that owns the device and returns its result."""
        future: concurrent.futures.Future = concurrent.futures.Future()
        with self._cv:
            if self._finished or self._stopping:
                raise WatcherClosed()
            self._requests.append((run, future))
            self._cv.notify_all()
        return future.result()

    def _add_listener(self, listener: Callable[[], None]) -> None:
        """Calls listener (on the watcher's thread) whenever an event arrives or the watcher stops."""
        with self._cv:
            self._listeners.append(listener)
            if self._out or self._finished:
                listener()

    def _wake(self) -> None:
        self._cv.notify_all()
        for listener in self._listeners:
            listener()

    # --- the thread that owns the device ----------------------------------------------------

    def _run(self) -> None:
        error: BaseException | None = None
        try:
            self._loop()
        except _Fatal as fatal:
            error = fatal.error
        except BaseException as unexpected:  # a bug; stop rather than hang
            error = unexpected
        self._drop()
        with self._cv:
            if error is not None:
                self._out.extend(self._pending)  # deliver what happened before the error
            else:
                self._out.clear()
            self._pending.clear()
            self._error = error
            self._finished = True
            requests = list(self._requests)
            self._requests.clear()
            self._wake()
        for _, future in requests:
            future.set_exception(WatcherClosed())

    def _loop(self) -> None:
        while True:
            with self._cv:
                while self._pending and len(self._out) < _OUT_CAPACITY:
                    self._out.append(self._pending.popleft())
                    self._wake()
                if self._stopping:
                    return
                requests = list(self._requests)
                self._requests.clear()
            for run, future in requests:
                try:
                    future.set_result(run())
                except _Fatal:
                    raise
                except BaseException as error:
                    future.set_exception(error)
            if requests:
                continue

            now = time.monotonic()
            if self._scan_at is not None and now >= self._scan_at:
                self._scan_at = None
                self._try_connect()
                continue
            if self._restore_at is not None and now >= self._restore_at:
                self._restore_at = None
                self._restore_target()
                continue
            if self._settle_at is not None and now >= self._settle_at:
                self._settle_at = None
                if self._conn is not None:
                    self._conn.awaiting_config = False
                    self._snapshot()
                continue

            deadlines = [t for t in (self._scan_at, self._restore_at, self._settle_at) if t is not None]
            wait = max(0.0, min(deadlines) - now) if deadlines else None
            if self._pending or self._conn is None:
                with self._cv:
                    if not self._requests and not self._stopping and not (
                        self._pending and len(self._out) < _OUT_CAPACITY
                    ):
                        self._cv.wait(wait)
                continue

            timeout = _READ_POLL if wait is None else min(_READ_POLL, wait)
            conn = self._conn
            try:
                data = conn.handle.read(_REPORT_LEN, max(1, int(timeout * 1000)))
            except Exception as error:
                self._lost(error)
                continue
            if data:
                self._received(bytes(data), time.monotonic())

    def _emit(self, event: Event) -> None:
        self._pending.append(event)

    def _failed(self, error: BaseException) -> None:
        """Notes a failed attempt to use the device. A cause is reported when it happens twice in a
        row, which skips the moment after plugging in when udev has not set the permissions yet."""
        if str(error) != self._failure:
            self._failure, self._failures = str(error), 0
        self._failures += 1
        if self._failures == 2:
            self._emit(ConnectFailed(error))

    def _reset_failures(self) -> None:
        self._failure, self._failures = "", 0

    def _try_connect(self) -> None:
        try:
            entries = self._bus.devices()
        except Exception as error:
            self._failed(error)
            self._scan_at = time.monotonic() + self._interval
            return
        matching = [entry for entry in entries if self._serial is None or entry.serial == self._serial]
        if not matching:
            self._reset_failures()
            self._scan_at = time.monotonic() + self._interval
            return
        if self._serial is None and len(matching) > 1:
            found = ", ".join(entry.serial for entry in matching)
            raise _Fatal(SeveralDevices(f"several devices are connected; set the serial number of the one to watch: {found}"))
        self._serial = matching[0].serial
        try:
            self._connect(matching[0])
        except _Fatal:
            raise
        except Exception as error:
            self._failed(error)
            self._scan_at = time.monotonic() + self._interval

    def _connect(self, entry: DeviceEntry) -> None:
        handle = self._bus.open(entry)
        connected = False
        try:
            device = Device(handle, serial=entry.serial)
            info = device.info
            self._info = info
            found = device.get_pin_config().config
            conn = _Connection(device, handle)
            driven: list[int] = []
            if self._target is not None and found != self._target:
                try:
                    device.set_pin_config(self._target)
                except PinConfigRejected as rejected:
                    raise _Fatal(rejected) from rejected
                driven = _driven_outputs(found, self._target)
                found = self._target
                conn.awaiting_config = True
            connected = True
        finally:
            if not connected:
                handle.close()

        self._conn = conn
        self._reset_failures()
        reconnected = self._ever_connected
        self._ever_connected = True
        self._set_config(found)
        self._emit(Connected(entry.serial, entry.path, info, found, reconnected))

        # Losing the connection always sends the outputs back to their initial level (PROTOCOL.md 7.2).
        outputs = protocol.mask_to_gpios(found.outputs_mask)
        if reconnected and outputs:
            self._emit(OutputsReset(outputs, OutputResetCause.RECONNECT))
        elif driven:
            self._emit(OutputsReset(driven, OutputResetCause.PIN_CONFIG))
        if conn.awaiting_config:
            self._settle_at = time.monotonic() + _SETTLE_TIMEOUT
            return
        self._snapshot()

    def _drop(self) -> None:
        """Closes the connection, if there is one."""
        conn, self._conn = self._conn, None
        if conn is None:
            return
        try:
            conn.handle.close()
        except Exception:
            pass
        self._restore_at = None
        self._settle_at = None

    def _lost(self, error: BaseException) -> None:
        """Handles a connection that failed, usually because the device was unplugged."""
        if self._conn is None:
            return
        self._drop()
        self._emit(Disconnected(error))
        self._scan_at = time.monotonic() + self._interval

    def _received(self, data: bytes, at: float) -> None:
        if len(data) < _REPORT_LEN or data[0] != protocol.REPORT_ID_STATUS:
            return
        try:
            report = protocol.decode_status(data[1:_REPORT_LEN])
        except ValueError:
            return
        conn = self._conn
        assert conn is not None
        missed = 0 if conn.last_seq is None else (report.seq - conn.last_seq - 1) & 0xFFFF
        conn.last_seq = report.seq
        self._status(report, at, missed)

    def _snapshot(self) -> None:
        """Asks the device for its current state and takes it as the reference for later reports."""
        conn = self._conn
        assert conn is not None
        try:
            report = conn.device.request_status()
        except Exception as error:
            self._lost(error)
            return
        # With edge events still queued, the levels are ahead of the interrupt reports that will
        # carry those events; learn from the interrupt reports instead, so no event counts twice.
        conn.snapshot_seq = report.seq if report.seq != 0 and not report.more_events else None
        self._status(report, time.monotonic(), 0)

    def _status(self, report: StatusReport, at: float, missed: int) -> None:
        conn = self._conn
        assert conn is not None
        conn.clock.add(at, report.timestamp_us)
        self._emit(StatusReceived(report, missed, self._on_off(report)))
        if report.reason & Reason.OUTPUT_RESET:
            outputs = protocol.mask_to_gpios(report.outputs)
            if outputs:
                self._emit(OutputsReset(outputs, OutputResetCause.SUSPEND))
        host_request = bool(report.reason & Reason.HOST_REQUEST)
        stale = False
        if not host_request and conn.snapshot_seq is not None:
            distance = (report.seq - conn.snapshot_seq) & 0xFFFF  # seq wraps around
            if distance == 0 or distance >= 0x8000:
                stale = True
            else:
                conn.snapshot_seq = None
        if report.reason & Reason.CONFIG_CHANGED:
            if not stale:
                conn.awaiting_config = False
                self._settle_at = None
            self._config_changed()
            if self._conn is None:
                return
        if stale or conn.awaiting_config or (host_request and report.more_events):
            return
        self._absorb(report)

    def _absorb(self, report: StatusReport) -> None:
        """Updates the known ON/OFF from a status report and reports what changed."""
        conn = self._conn
        assert conn is not None
        for event in report.events:
            gpio = event.gpio
            if gpio not in self._known:
                continue
            was = self._known[gpio]
            on = event.level != self._is_active_low(gpio)
            if on == was:
                # An edge event always leaves the level before it, so the opposite change was lost.
                self._emit(OnOffChange(gpio, not on, not event.level, inferred=True))
            start = event.start_us(report.timestamp_us)
            if start is None:
                self._emit(OnOffChange(gpio, on, event.level))
            else:
                self._emit(OnOffChange(gpio, on, event.level, time=conn.clock.at(start), device_us=start))
            self._known[gpio] = on
        learned: dict[int, bool] = {}
        for gpio in protocol.mask_to_gpios(report.monitored):
            on = report.level(gpio) != self._is_active_low(gpio)
            if gpio not in self._known:
                learned[gpio] = on
            elif self._known[gpio] != on:
                self._emit(OnOffChange(gpio, on, report.level(gpio), inferred=True))
            self._known[gpio] = on
        for gpio in list(self._known):
            if not report.is_monitored(gpio):
                del self._known[gpio]
        if learned:
            self._emit(InitialOnOff(learned))

    def _is_active_low(self, gpio: int) -> bool:
        if gpio in self._active_low:
            return self._active_low[gpio]
        return self._config[gpio].mode == protocol.PinMode.PULLUP

    def _on_off(self, report: StatusReport) -> dict[int, bool]:
        return {
            gpio: report.level(gpio) != self._is_active_low(gpio) for gpio in protocol.mask_to_gpios(report.monitored)
        }

    def _set_config(self, config: PinConfig) -> None:
        """Records the pin configuration of the device and forgets the ON/OFF of pins that are no
        longer monitored or whose pull changed: their pin level is read anew (PROTOCOL.md 6.4)."""
        for gpio in list(self._known):
            if not config[gpio].is_monitored or config[gpio].mode != self._config[gpio].mode:
                del self._known[gpio]
        self._config = config

    def _config_changed(self) -> None:
        """Follows a new configuration, or writes the target back when another program changed it."""
        conn = self._conn
        assert conn is not None
        try:
            config = conn.device.get_pin_config().config
        except Exception as error:
            self._lost(error)
            return
        changed = config != self._config
        self._set_config(config)
        if self._target is None:
            if changed:
                self._emit(PinConfigChanged(config))
            return
        if config == self._target:
            return
        wait = _RESTORE_INTERVAL - (time.monotonic() - self._last_restore)
        if wait > 0:
            self._restore_at = time.monotonic() + wait
            return
        self._restore_target()

    def _restore_target(self) -> None:
        conn = self._conn
        if conn is None or self._target is None:
            return
        self._restore_at = None
        self._last_restore = time.monotonic()
        try:
            found = conn.device.get_pin_config().config
        except Exception as error:
            self._lost(error)
            return
        self._set_config(found)
        if found == self._target:
            return
        try:
            conn.device.set_pin_config(self._target)
        except PinConfigRejected as rejected:
            raise _Fatal(rejected) from rejected
        except PinConfigConflict:
            self._restore_at = time.monotonic() + _RESTORE_INTERVAL
            return
        except Exception as error:
            self._lost(error)
            return
        self._restore_count += 1
        self._emit(PinConfigRestored(found, self._restore_count))
        self._wrote(found, self._target)

    def _set_target(self, config: PinConfig) -> None:
        if self._info is not None:
            result, gpio = protocol.validate_pin_config(config, self._info.available)
            if result != Result.OK:
                raise PinConfigRejected(result, gpio, local=True)
        previous = self._target
        self._target = config
        conn = self._conn
        if conn is None:
            return
        self._restore_at = None
        found = conn.device.get_pin_config().config
        self._set_config(found)
        if found == config:
            return
        try:
            conn.device.set_pin_config(config)
        except PinConfigRejected:
            self._target = previous
            raise
        except PinConfigConflict:
            self._restore_at = time.monotonic() + _RESTORE_INTERVAL
            raise
        self._wrote(found, config)

    def _wrote(self, found: PinConfig, config: PinConfig) -> None:
        """Records that config was written over found."""
        assert self._conn is not None
        self._set_config(config)
        self._conn.awaiting_config = True
        self._settle_at = time.monotonic() + _SETTLE_TIMEOUT
        driven = _driven_outputs(found, config)
        if driven:
            self._emit(OutputsReset(driven, OutputResetCause.PIN_CONFIG))


def _driven_outputs(prev: PinConfig, new: PinConfig) -> list[int]:
    """The pins that writing new over prev drives to their initial output level: pins that become
    outputs and outputs whose initial output level changes (PROTOCOL.md 6.4)."""
    return [gpio for gpio in range(protocol.GPIO_COUNT) if new[gpio].is_output and prev[gpio] != new[gpio]]


def watch(
    serial: str | None = None,
    *,
    target_pins: PinConfig | None = None,
    active_low: dict[int, bool] | None = None,
    scan_interval: float = DEFAULT_SCAN_INTERVAL,
    bus: Bus | None = None,
) -> Watcher:
    """Starts watching a device and returns at once; the device need not be connected yet.

    serial: the device to watch; None takes the first one found and from then on waits for that
    one only, and stops the watcher with SeveralDevices if several are found at once.
    target_pins: the target pin configuration, written whenever the device has another one. Build
    it with PinConfig.unused().with_pins(...); pins it does not set are unused. None never writes
    the pin configuration and follows the one on the device.
    active_low: polarity overrides, True meaning LOW is ON. Other monitored pins are active low
    when they have the pull-up.
    scan_interval: seconds between looks for the device while it is disconnected.
    bus: finds and opens the device; None means the operating system (SystemBus).
    """
    if scan_interval <= 0:
        raise ValueError(f"the scan interval must be positive: {scan_interval}")
    if target_pins is not None:
        result, gpio = protocol.validate_pin_config(target_pins, _ALL_GPIOS)
        if result != Result.OK:
            raise PinConfigRejected(result, gpio, local=True)
    polarity = dict(active_low or {})
    for gpio in polarity:
        if not 0 <= gpio < protocol.GPIO_COUNT:
            raise ValueError(f"GPIO{gpio} is out of range (0-{protocol.GPIO_COUNT - 1})")
    if bus is None:
        usb_ids()  # fail at once on a bad HIDPIN_VID / HIDPIN_PID
        bus = SystemBus()
    return Watcher(
        bus=bus, serial=serial, target_pins=target_pins, active_low=polarity, scan_interval=scan_interval
    )


# --- asyncio --------------------------------------------------------------------------


class AsyncWatcher:
    """A Watcher for asyncio: take the events with `async for`, and await the methods.

    It runs the same thread as Watcher; the event loop only waits for its events.
    """

    def __init__(self, watcher: Watcher) -> None:
        self._watcher = watcher
        self._loop = asyncio.get_running_loop()
        self._ready = asyncio.Event()
        watcher._add_listener(self._notify)

    def _notify(self) -> None:
        try:
            self._loop.call_soon_threadsafe(self._ready.set)
        except RuntimeError:
            pass  # the event loop has been closed

    async def get(self) -> Event:
        """Takes the next event. Raises WatcherClosed once the watcher has stopped and every event
        has been taken."""
        while True:
            self._ready.clear()
            event = self._watcher.get(timeout=0)
            if event is not None:
                return event
            await self._ready.wait()

    def __aiter__(self) -> AsyncWatcher:
        return self

    async def __anext__(self) -> Event:
        try:
            return await self.get()
        except WatcherClosed:
            raise StopAsyncIteration from None

    @property
    def error(self) -> BaseException | None:
        return self._watcher.error

    async def set_outputs(self, levels: dict[int, bool]) -> None:
        await asyncio.to_thread(self._watcher.set_outputs, levels)

    async def set_target_pins(self, config: PinConfig) -> None:
        await asyncio.to_thread(self._watcher.set_target_pins, config)

    async def close(self) -> None:
        await asyncio.to_thread(self._watcher.close)

    async def __aenter__(self) -> AsyncWatcher:
        return self

    async def __aexit__(
        self, exc_type: type[BaseException] | None, exc: BaseException | None, tb: TracebackType | None
    ) -> None:
        await self.close()


def async_watch(
    serial: str | None = None,
    *,
    target_pins: PinConfig | None = None,
    active_low: dict[int, bool] | None = None,
    scan_interval: float = DEFAULT_SCAN_INTERVAL,
    bus: Bus | None = None,
) -> AsyncWatcher:
    """Like watch(), for asyncio; call it from a running event loop."""
    asyncio.get_running_loop()  # fail before starting the thread
    return AsyncWatcher(
        watch(serial, target_pins=target_pins, active_low=active_low, scan_interval=scan_interval, bus=bus)
    )
