"""Host library for hidpin devices (see docs/PROTOCOL.md)."""

from hidpin.device import (
    Device,
    DeviceEntry,
    HidpinError,
    PinConfigConflict,
    PinConfigRejected,
    ProtocolVersionError,
    find_devices,
)
from hidpin.protocol import (
    PROTOCOL_VERSION,
    Board,
    DeviceInfo,
    EdgeEvent,
    PinConfig,
    PinConfigReport,
    PinMode,
    PinSetting,
    Reason,
    Result,
    StatusFlags,
    StatusReport,
)

__all__ = [
    "PROTOCOL_VERSION",
    "Board",
    "Device",
    "DeviceEntry",
    "DeviceInfo",
    "EdgeEvent",
    "HidpinError",
    "PinConfig",
    "PinConfigConflict",
    "PinConfigRejected",
    "PinConfigReport",
    "PinMode",
    "PinSetting",
    "ProtocolVersionError",
    "Reason",
    "Result",
    "StatusFlags",
    "StatusReport",
    "find_devices",
]
