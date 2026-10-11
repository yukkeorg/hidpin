"""Simulated hidpin boards, to test code that uses hidpin without hardware.

FakeBus stands in for the USB ports of the host and can be passed to hidpin.watch() as its bus;
FakeBoard follows docs/PROTOCOL.md closely enough for the Watch layer. They match the Go package
hidpintest.
"""

from __future__ import annotations

import dataclasses
import errno
import threading
import time
from collections import deque

from hidpin import protocol
from hidpin.device import DeviceEntry
from hidpin.protocol import (
    DeviceInfo,
    EdgeEvent,
    PinConfig,
    PinConfigReport,
    Reason,
    Result,
    StatusFlags,
    StatusReport,
)

PICO_AVAILABLE = 0x1C7FFFFF  # GPIO0-22 and 26-28 of the Raspberry Pi Pico

_QUEUE_LEN = 64  # reports a handle keeps for the host; later ones are dropped, as hidraw does


class DeviceGone(OSError):
    """What the handles of an unplugged board raise, like ENODEV from hidraw."""

    def __init__(self) -> None:
        super().__init__(errno.ENODEV, "the device is gone")


class FakeBus:
    """A bus with boards that can be plugged in and out while a watcher runs."""

    def __init__(self, *boards: FakeBoard) -> None:
        self._lock = threading.Lock()
        self._boards: list[FakeBoard] = []
        self._nodes = 0
        for board in boards:
            self.plug(board)

    def devices(self) -> list[DeviceEntry]:
        with self._lock:
            return [
                DeviceEntry(path=board.path.encode(), serial=board.serial, manufacturer="yukke.org", product="hidpin")
                for board in self._boards
            ]

    def open(self, entry: DeviceEntry) -> _Handle:
        with self._lock:
            for board in self._boards:
                if board.path.encode() == entry.path:
                    return board._open()
        raise FileNotFoundError(errno.ENOENT, f"cannot open {entry.path.decode()}")

    def plug(self, board: FakeBoard) -> None:
        """Connects a board. A board that was unplugged starts afresh, as it lost power."""
        with self._lock:
            board._plug(f"/dev/hidraw{self._nodes}")
            self._nodes += 1
            self._boards.append(board)

    def unplug(self, board: FakeBoard) -> None:
        """Disconnects a board: its open handles fail with DeviceGone and it loses power."""
        with self._lock:
            self._boards.remove(board)
            board._unplug()


class FakeBoard:
    """A Raspberry Pi Pico running the hidpin firmware.

    Every GPIO input starts HIGH, like an open switch with a pull-up. Status reports go to the
    handles that are open when they are sent.
    """

    def __init__(self, serial: str) -> None:
        self.serial = serial
        self.path = ""
        self._lock = threading.Lock()
        self._powered = False
        self._open_error: BaseException | None = None
        self._info = DeviceInfo(
            protocol_version=protocol.PROTOCOL_VERSION,
            firmware=(0, 1, 0),
            board=protocol.Board.PICO,
            gpio_count=protocol.GPIO_COUNT,
            periodic_interval_ms=1000,
            available=PICO_AVAILABLE,
            events_per_report=protocol.EVENTS_PER_REPORT,
            event_queue_size=32,
        )
        self._config = PinConfig.default(PICO_AVAILABLE)
        self._result = Result.OK
        self._result_gpio = protocol.RESULT_GPIO_NONE
        self._request_id = 0
        self._inputs = PICO_AVAILABLE  # the electrical value of every GPIO
        self._levels = 0  # pin levels of monitored pins and output levels of output pins
        self._next_seq = 0
        self._last_seq = 0
        self._clock_us = 0
        self._overflow = False
        self._handles: list[_Handle] = []

    # --- the bus -------------------------------------------------------------------

    def _plug(self, path: str) -> None:
        with self._lock:
            self.path = path
            if self._powered:
                return
            self._powered = True
            self._config = PinConfig.default(self._info.available)
            self._result, self._result_gpio, self._request_id = Result.OK, protocol.RESULT_GPIO_NONE, 0
            self._levels = self._inputs & self._config.monitored_mask
            # The host sees the board some seconds after it started.
            self._next_seq, self._last_seq, self._clock_us, self._overflow = 0, 0, 5_000_000, False

    def _unplug(self) -> None:
        with self._lock:
            self._fail_handles()
            self._powered = False
            self.path = ""

    def _fail_handles(self) -> None:
        for handle in self._handles:
            handle._fail()
        self._handles = []

    def _open(self) -> _Handle:
        with self._lock:
            if self._open_error is not None:
                raise self._open_error
            handle = _Handle(self)
            self._handles.append(handle)
            return handle

    # --- controls for tests ----------------------------------------------------------

    def set_open_error(self, error: BaseException | None) -> None:
        """Makes opening the board fail with error, for example PermissionError; None lets it open."""
        with self._lock:
            self._open_error = error

    def set_protocol_version(self, version: int) -> None:
        with self._lock:
            self._info = dataclasses.replace(self._info, protocol_version=version)

    def set_input(self, gpio: int, high: bool) -> None:
        """Changes the electrical value of a GPIO. On a monitored pin this changes the pin level and
        sends an edge event, which began the debounce time before it was reported."""
        with self._lock:
            bit = 1 << gpio
            self._inputs = self._inputs & ~bit | (bit if high else 0)
            if not self._powered or not self._config[gpio].is_monitored or self._levels & bit == self._inputs & bit:
                return
            self._levels = self._levels & ~bit | self._inputs & bit
            age = self._config[gpio].param * 1000
            self._send(Reason.LEVEL_CHANGED, (EdgeEvent(gpio=gpio, level=high, age_us=age),))

    def lose_input(self, gpio: int, high: bool) -> None:
        """Changes a monitored pin as if its edge event were dropped from a full event queue: the
        next report carries the new level and OVERFLOW."""
        with self._lock:
            bit = 1 << gpio
            self._inputs = self._inputs & ~bit | (bit if high else 0)
            if self._config[gpio].is_monitored:
                self._levels = self._levels & ~bit | self._inputs & bit
                self._overflow = True

    def periodic(self) -> None:
        """Sends a periodic status report."""
        with self._lock:
            self._send(Reason.PERIODIC, ())

    def skip_reports(self, n: int) -> None:
        """Sends n status reports that the host never reads."""
        with self._lock:
            self._next_seq = (self._next_seq + n) & 0xFFFF
            self._last_seq = (self._next_seq - 1) & 0xFFFF

    def suspend(self) -> None:
        """A USB suspend: outputs go back to their initial output level and, if there are any, the
        next report says OUTPUT_RESET."""
        with self._lock:
            if self._reset_outputs():
                self._send(Reason.OUTPUT_RESET, ())

    def reenumerate(self) -> None:
        """A USB reset that makes the host create the device node anew without the board losing
        power: open handles fail, and outputs go back to their initial output level."""
        with self._lock:
            self._fail_handles()
            self._reset_outputs()

    def write_pin_config(self, config: PinConfig) -> Result:
        """Writes a pin configuration as another program would."""
        with self._lock:
            self._write_config(protocol.encode_pin_config_set(config, 1))
            return self._result

    @property
    def pin_config(self) -> PinConfig:
        with self._lock:
            return self._config

    def level(self, gpio: int) -> bool:
        """The pin level or output level of a GPIO."""
        with self._lock:
            return bool(self._levels >> gpio & 1)

    @property
    def open_count(self) -> int:
        """The number of open handles."""
        with self._lock:
            return len(self._handles)

    # --- the firmware -----------------------------------------------------------------

    def _reset_outputs(self) -> bool:
        outputs = self._config.outputs_mask
        for gpio in protocol.mask_to_gpios(outputs):
            bit = 1 << gpio
            self._levels = self._levels & ~bit | (bit if self._config[gpio].param else 0)
        return outputs != 0

    def _status(self, reason: Reason, events: tuple[EdgeEvent, ...], seq: int, flags: StatusFlags) -> StatusReport:
        return StatusReport(
            seq=seq,
            reason=reason,
            flags=flags,
            monitored=self._config.monitored_mask,
            outputs=self._config.outputs_mask,
            levels=self._levels,
            timestamp_us=self._clock_us,
            events=events,
        )

    def _send(self, reason: Reason, events: tuple[EdgeEvent, ...]) -> None:
        self._clock_us += 1000
        flags = StatusFlags.OVERFLOW if self._overflow else StatusFlags(0)
        self._overflow = False
        report = self._status(reason, events, self._next_seq, flags)
        self._last_seq = self._next_seq
        self._next_seq = (self._next_seq + 1) & 0xFFFF
        data = bytes([protocol.REPORT_ID_STATUS]) + protocol.encode_status(report)
        for handle in self._handles:
            handle._deliver(data)

    def _write_config(self, payload: bytes) -> None:
        result, gpio, request_id = protocol.validate_pin_config_payload(payload, self._info.available)
        self._result, self._result_gpio, self._request_id = result, gpio, request_id
        if result != Result.OK:
            return
        new = protocol.decode_pin_config(payload).config
        if new == self._config:
            return
        for gpio in range(protocol.GPIO_COUNT):
            prev, setting, bit = self._config[gpio], new[gpio], 1 << gpio
            if prev == setting:
                continue
            if prev.is_monitored and setting.is_monitored and prev.mode == setting.mode:
                continue  # only the debounce time changed: the pin level stays
            if setting.is_monitored:
                self._levels = self._levels & ~bit | self._inputs & bit
            elif setting.is_output:
                self._levels = self._levels & ~bit | (bit if setting.param else 0)
            else:
                self._levels &= ~bit
        self._config = new
        self._send(Reason.CONFIG_CHANGED, ())


class _Handle:
    """One open handle of a board, with the methods of hid.device."""

    def __init__(self, board: FakeBoard) -> None:
        self._board = board
        self._cv = threading.Condition()
        self._reports: deque[bytes] = deque()
        self._closed = False
        self._failed = False

    def _deliver(self, data: bytes) -> None:
        with self._cv:
            if len(self._reports) < _QUEUE_LEN:
                self._reports.append(data)
                self._cv.notify_all()

    def _fail(self) -> None:
        with self._cv:
            self._failed = True
            self._cv.notify_all()

    def _check(self) -> None:
        if self._closed:
            raise OSError(errno.EBADF, "the handle is closed")
        if self._failed:
            raise DeviceGone()

    def read(self, length: int, timeout_ms: int = 0) -> list[int]:
        deadline = None if timeout_ms < 0 else time.monotonic() + timeout_ms / 1000
        with self._cv:
            while True:
                self._check()
                if self._reports:
                    return list(self._reports.popleft()[:length])
                remaining = None if deadline is None else deadline - time.monotonic()
                if remaining is not None and remaining <= 0:
                    return []
                self._cv.wait(remaining)

    def write(self, data) -> int:
        with self._cv:
            self._check()
        data = bytes(data)
        board = self._board
        with board._lock:
            if data[0] != protocol.REPORT_ID_OUTPUT:
                raise OSError(errno.EINVAL, "unexpected output report")
            try:
                mask, value = protocol.decode_output(data[1:])
            except ValueError:
                return len(data)  # the firmware ignores it
            mask &= board._config.outputs_mask
            board._levels = board._levels & ~mask | value & mask
            board._send(Reason.OUTPUT_APPLIED, ())
        return len(data)

    def get_feature_report(self, report_id: int, length: int) -> list[int]:
        with self._cv:
            self._check()
        board = self._board
        with board._lock:
            if report_id == protocol.REPORT_ID_DEVICE_INFO:
                payload = protocol.encode_device_info(board._info)
            elif report_id == protocol.REPORT_ID_PIN_CONFIG:
                payload = protocol.encode_pin_config(
                    PinConfigReport(board._result, board._result_gpio, board._request_id, board._config)
                )
            else:
                raise OSError(errno.EINVAL, "unexpected feature report")
        return list((bytes([report_id]) + payload)[:length])

    def send_feature_report(self, data) -> int:
        with self._cv:
            self._check()
        data = bytes(data)
        board = self._board
        with board._lock:
            if data[0] != protocol.REPORT_ID_PIN_CONFIG:
                raise OSError(errno.EINVAL, "unexpected feature report")
            board._write_config(data[1:])
        return len(data)

    def get_input_report(self, report_id: int, length: int) -> list[int]:
        with self._cv:
            self._check()
        board = self._board
        with board._lock:
            report = board._status(Reason.HOST_REQUEST, (), board._last_seq, StatusFlags(0))
        return list((bytes([report_id]) + protocol.encode_status(report))[:length])

    def close(self) -> None:
        with self._cv:
            self._closed = True
            self._cv.notify_all()
        board = self._board
        with board._lock:
            if self in board._handles:
                board._handles.remove(self)
