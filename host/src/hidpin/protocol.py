"""Wire format of hidpin protocol version 1 (docs/PROTOCOL.md).

All payloads here exclude the report ID byte, exactly like the C core in firmware/core.
"""

from __future__ import annotations

import struct
from dataclasses import dataclass
from enum import IntEnum, IntFlag

PROTOCOL_VERSION = 1

REPORT_ID_STATUS = 0x01
REPORT_ID_DEVICE_INFO = 0x02
REPORT_ID_PIN_CONFIG = 0x03
REPORT_ID_OUTPUT = 0x04

REPORT_PAYLOAD_LEN = 63
OUTPUT_PAYLOAD_LEN = 8

GPIO_COUNT = 30
GPIO_MASK = (1 << GPIO_COUNT) - 1
EVENTS_PER_REPORT = 7
EVENT_LEN = 5
STATUS_EVENTS_OFFSET = 28
DEFAULT_DEBOUNCE_MS = 20
SATURATED_AGE_US = 0xFFFFFFFF
RESULT_GPIO_NONE = 0xFF


class Reason(IntFlag):
    """Why the device sent a status report (PROTOCOL.md 4.1)."""

    LEVEL_CHANGED = 0x01
    OUTPUT_APPLIED = 0x02
    CONFIG_CHANGED = 0x04
    PERIODIC = 0x08
    OUTPUT_RESET = 0x10
    HOST_REQUEST = 0x80


class StatusFlags(IntFlag):
    """Auxiliary flags of a status report (PROTOCOL.md 4.2)."""

    OVERFLOW = 0x01
    MORE_EVENTS = 0x02


class PinMode(IntEnum):
    """How the device uses one GPIO (PROTOCOL.md 6.1)."""

    UNUSED = 0
    INPUT = 1
    PULLUP = 2
    PULLDOWN = 3
    OUTPUT = 4

    @property
    def is_input(self) -> bool:
        return self in (PinMode.INPUT, PinMode.PULLUP, PinMode.PULLDOWN)


class Result(IntEnum):
    """Outcome of the last pin configuration Set (PROTOCOL.md 6.3)."""

    OK = 0
    BAD_LENGTH = 1
    BAD_MODE = 2
    UNAVAILABLE_GPIO = 3
    UNUSED_WITH_PARAM = 4
    BAD_OUTPUT_LEVEL = 5


RESULT_MESSAGES = {
    Result.OK: "適用した",
    Result.BAD_LENGTH: "ペイロード長が63バイトではない",
    Result.BAD_MODE: "modeが0-4の範囲外",
    Result.UNAVAILABLE_GPIO: "利用可能GPIOでないピンを使おうとした",
    Result.UNUSED_WITH_PARAM: "使わないピンにparamが指定されている",
    Result.BAD_OUTPUT_LEVEL: "出力ピンの初期出力レベルが0か1ではない",
}


class Board(IntEnum):
    """Board type reported in the device information (PROTOCOL.md 5.1)."""

    PICO = 1
    QTPY_RP2040 = 2


BOARD_NAMES = {
    Board.PICO: "Raspberry Pi Pico",
    Board.QTPY_RP2040: "Adafruit QT Py RP2040",
}


def mask_to_gpios(mask: int) -> list[int]:
    return [gpio for gpio in range(GPIO_COUNT) if mask >> gpio & 1]


def board_name(board: int) -> str:
    try:
        return BOARD_NAMES[Board(board)]
    except ValueError:
        return f"unknown board {board}"


@dataclass(frozen=True)
class EdgeEvent:
    """One confirmed change of a pin level (PROTOCOL.md 4.4)."""

    gpio: int
    level: bool
    age_us: int

    @property
    def saturated(self) -> bool:
        """True when the change is older than the 32 bit age can express."""
        return self.age_us == SATURATED_AGE_US

    def start_us(self, report_timestamp_us: int) -> int | None:
        if self.saturated:
            return None
        return report_timestamp_us - self.age_us


@dataclass(frozen=True)
class StatusReport:
    """Status report, report ID 1 (PROTOCOL.md 4)."""

    seq: int
    reason: Reason
    flags: StatusFlags
    monitored: int
    outputs: int
    levels: int
    timestamp_us: int
    events: tuple[EdgeEvent, ...] = ()

    @property
    def overflow(self) -> bool:
        return bool(self.flags & StatusFlags.OVERFLOW)

    @property
    def more_events(self) -> bool:
        return bool(self.flags & StatusFlags.MORE_EVENTS)

    def level(self, gpio: int) -> bool:
        return bool(self.levels >> gpio & 1)

    def is_monitored(self, gpio: int) -> bool:
        return bool(self.monitored >> gpio & 1)

    def is_output(self, gpio: int) -> bool:
        return bool(self.outputs >> gpio & 1)


def decode_status(payload: bytes) -> StatusReport:
    if len(payload) != REPORT_PAYLOAD_LEN:
        raise ValueError(f"status payload must be {REPORT_PAYLOAD_LEN} bytes, got {len(payload)}")
    seq, reason, flags, count = struct.unpack_from("<HBBB", payload, 0)
    monitored, outputs, levels, timestamp_us = struct.unpack_from("<IIIQ", payload, 8)
    if count > EVENTS_PER_REPORT:
        raise ValueError(f"event_count {count} exceeds {EVENTS_PER_REPORT}")
    events = []
    for i in range(count):
        pin, age_us = struct.unpack_from("<BI", payload, STATUS_EVENTS_OFFSET + EVENT_LEN * i)
        events.append(EdgeEvent(gpio=pin & 0x1F, level=bool(pin & 0x80), age_us=age_us))
    return StatusReport(
        seq=seq,
        reason=Reason(reason),
        flags=StatusFlags(flags),
        monitored=monitored,
        outputs=outputs,
        levels=levels,
        timestamp_us=timestamp_us,
        events=tuple(events),
    )


def encode_status(report: StatusReport) -> bytes:
    if len(report.events) > EVENTS_PER_REPORT:
        raise ValueError(f"at most {EVENTS_PER_REPORT} events fit in one report")
    payload = bytearray(REPORT_PAYLOAD_LEN)
    struct.pack_into("<HBBB", payload, 0, report.seq, int(report.reason), int(report.flags), len(report.events))
    struct.pack_into("<IIIQ", payload, 8, report.monitored, report.outputs, report.levels, report.timestamp_us)
    for i, event in enumerate(report.events):
        pin = (event.gpio & 0x1F) | (0x80 if event.level else 0x00)
        struct.pack_into("<BI", payload, STATUS_EVENTS_OFFSET + EVENT_LEN * i, pin, event.age_us)
    return bytes(payload)


@dataclass(frozen=True)
class DeviceInfo:
    """Device information, report ID 2 (PROTOCOL.md 5)."""

    protocol_version: int
    firmware: tuple[int, int, int]
    board: int
    gpio_count: int
    periodic_interval_ms: int
    available: int
    events_per_report: int
    event_queue_size: int

    @property
    def board_name(self) -> str:
        return board_name(self.board)

    @property
    def firmware_version(self) -> str:
        return "{}.{}.{}".format(*self.firmware)

    @property
    def available_gpios(self) -> list[int]:
        return mask_to_gpios(self.available)


def decode_device_info(payload: bytes) -> DeviceInfo:
    if len(payload) != REPORT_PAYLOAD_LEN:
        raise ValueError(f"device info payload must be {REPORT_PAYLOAD_LEN} bytes, got {len(payload)}")
    version, major, minor, patch, board, gpio_count = struct.unpack_from("<BBBBBB", payload, 0)
    interval, available = struct.unpack_from("<HI", payload, 6)
    events_per_report, queue_size = struct.unpack_from("<BB", payload, 12)
    return DeviceInfo(
        protocol_version=version,
        firmware=(major, minor, patch),
        board=board,
        gpio_count=gpio_count,
        periodic_interval_ms=interval,
        available=available,
        events_per_report=events_per_report,
        event_queue_size=queue_size,
    )


def encode_device_info(info: DeviceInfo) -> bytes:
    payload = bytearray(REPORT_PAYLOAD_LEN)
    struct.pack_into(
        "<BBBBBB", payload, 0, info.protocol_version, *info.firmware, info.board, info.gpio_count
    )
    struct.pack_into("<HI", payload, 6, info.periodic_interval_ms, info.available)
    struct.pack_into("<BB", payload, 12, info.events_per_report, info.event_queue_size)
    return bytes(payload)


@dataclass(frozen=True)
class PinSetting:
    """How one GPIO is used: a mode plus its parameter (PROTOCOL.md 6.1)."""

    mode: PinMode = PinMode.UNUSED
    param: int = 0

    @classmethod
    def unused(cls) -> "PinSetting":
        return cls(PinMode.UNUSED, 0)

    @classmethod
    def monitor(cls, mode: PinMode = PinMode.PULLUP, debounce_ms: int = DEFAULT_DEBOUNCE_MS) -> "PinSetting":
        if not mode.is_input:
            raise ValueError(f"{mode.name} is not an input mode")
        return cls(mode, debounce_ms)

    @classmethod
    def output(cls, level: bool = False) -> "PinSetting":
        return cls(PinMode.OUTPUT, 1 if level else 0)

    @property
    def is_monitored(self) -> bool:
        return self.mode.is_input

    @property
    def is_output(self) -> bool:
        return self.mode == PinMode.OUTPUT

    @property
    def debounce_ms(self) -> int:
        if not self.is_monitored:
            raise ValueError("debounce time applies to monitored pins only")
        return self.param

    @property
    def initial_level(self) -> bool:
        if not self.is_output:
            raise ValueError("initial output level applies to output pins only")
        return bool(self.param)

    def __str__(self) -> str:
        if self.mode == PinMode.UNUSED:
            return "off"
        if self.mode == PinMode.OUTPUT:
            return "out:high" if self.param else "out:low"
        names = {PinMode.INPUT: "nopull", PinMode.PULLUP: "pullup", PinMode.PULLDOWN: "pulldown"}
        return f"{names[self.mode]}:{self.param}"


@dataclass(frozen=True)
class PinConfig:
    """Pin configuration of every GPIO."""

    pins: tuple[PinSetting, ...]

    def __post_init__(self) -> None:
        if len(self.pins) != GPIO_COUNT:
            raise ValueError(f"pin configuration needs {GPIO_COUNT} entries, got {len(self.pins)}")

    @classmethod
    def default(cls, available: int) -> "PinConfig":
        """The configuration the device starts with (PROTOCOL.md 6.1)."""
        pins = [
            PinSetting.monitor(PinMode.PULLUP, DEFAULT_DEBOUNCE_MS) if available >> gpio & 1 else PinSetting.unused()
            for gpio in range(GPIO_COUNT)
        ]
        return cls(tuple(pins))

    def __getitem__(self, gpio: int) -> PinSetting:
        return self.pins[gpio]

    def with_pin(self, gpio: int, setting: PinSetting) -> "PinConfig":
        if not 0 <= gpio < GPIO_COUNT:
            raise ValueError(f"GPIO{gpio} is out of range")
        pins = list(self.pins)
        pins[gpio] = setting
        return PinConfig(tuple(pins))

    def with_pins(self, settings: dict[int, PinSetting]) -> "PinConfig":
        config = self
        for gpio, setting in settings.items():
            config = config.with_pin(gpio, setting)
        return config

    @property
    def monitored_mask(self) -> int:
        return sum(1 << gpio for gpio, pin in enumerate(self.pins) if pin.is_monitored)

    @property
    def outputs_mask(self) -> int:
        return sum(1 << gpio for gpio, pin in enumerate(self.pins) if pin.is_output)


@dataclass(frozen=True)
class PinConfigReport:
    """Pin configuration report, report ID 3 (PROTOCOL.md 6)."""

    result: Result
    result_gpio: int
    request_id: int
    config: PinConfig


def decode_pin_config(payload: bytes) -> PinConfigReport:
    if len(payload) != REPORT_PAYLOAD_LEN:
        raise ValueError(f"pin config payload must be {REPORT_PAYLOAD_LEN} bytes, got {len(payload)}")
    result, result_gpio, request_id = payload[0], payload[1], payload[2]
    pins = []
    for gpio in range(GPIO_COUNT):
        mode = payload[3 + 2 * gpio]
        param = payload[4 + 2 * gpio]
        if mode > PinMode.OUTPUT:
            raise ValueError(f"GPIO{gpio} の mode {mode} は未定義です")
        pins.append(PinSetting(PinMode(mode), param))
    try:
        result_value = Result(result)
    except ValueError:
        result_value = result
    return PinConfigReport(
        result=result_value, result_gpio=result_gpio, request_id=request_id, config=PinConfig(tuple(pins))
    )


def encode_pin_config(report: PinConfigReport) -> bytes:
    payload = bytearray(REPORT_PAYLOAD_LEN)
    payload[0] = int(report.result)
    payload[1] = report.result_gpio
    payload[2] = report.request_id
    for gpio, pin in enumerate(report.config.pins):
        payload[3 + 2 * gpio] = int(pin.mode)
        payload[4 + 2 * gpio] = pin.param
    return bytes(payload)


def encode_pin_config_set(config: PinConfig, request_id: int) -> bytes:
    """Builds the payload the host writes; result fields are ignored by the device."""
    if not 1 <= request_id <= 255:
        raise ValueError("request_id must be between 1 and 255")
    return encode_pin_config(PinConfigReport(Result.OK, RESULT_GPIO_NONE, request_id, config))


def validate_pin_config_payload(payload: bytes, available: int) -> tuple[Result, int, int]:
    """Repeats the device-side validation of a Set payload (PROTOCOL.md 6.3).

    Returns the result, the GPIO it refers to, and the request_id the device records.
    """
    request_id = payload[2] if len(payload) >= 3 else 0
    if len(payload) != REPORT_PAYLOAD_LEN:
        return Result.BAD_LENGTH, RESULT_GPIO_NONE, request_id
    for gpio in range(GPIO_COUNT):
        mode = payload[3 + 2 * gpio]
        param = payload[4 + 2 * gpio]
        if mode > PinMode.OUTPUT:
            return Result.BAD_MODE, gpio, request_id
        if not available >> gpio & 1 and mode != PinMode.UNUSED:
            return Result.UNAVAILABLE_GPIO, gpio, request_id
        if mode == PinMode.UNUSED and param != 0:
            return Result.UNUSED_WITH_PARAM, gpio, request_id
        if mode == PinMode.OUTPUT and param > 1:
            return Result.BAD_OUTPUT_LEVEL, gpio, request_id
    return Result.OK, RESULT_GPIO_NONE, request_id


def validate_pin_config(config: PinConfig, available: int) -> tuple[Result, int]:
    """Validates a typed configuration so the CLI can fail before writing to the device."""
    payload = encode_pin_config(PinConfigReport(Result.OK, RESULT_GPIO_NONE, 1, config))
    result, gpio, _ = validate_pin_config_payload(payload, available)
    return result, gpio


def encode_output(mask: int, value: int) -> bytes:
    """Output report payload, report ID 4 (PROTOCOL.md 7.1)."""
    return struct.pack("<II", mask & 0xFFFFFFFF, value & 0xFFFFFFFF)


def decode_output(payload: bytes) -> tuple[int, int]:
    if len(payload) != OUTPUT_PAYLOAD_LEN:
        raise ValueError(f"output payload must be {OUTPUT_PAYLOAD_LEN} bytes, got {len(payload)}")
    return struct.unpack("<II", payload)
