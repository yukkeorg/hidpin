"""Talking to a hidpin device over USB HID."""

from __future__ import annotations

import os
import random
from dataclasses import dataclass
from types import TracebackType

from hidpin import _hidraw, protocol
from hidpin.protocol import (
    DeviceInfo,
    PinConfig,
    PinConfigReport,
    PinMode,
    PinSetting,
    Reason,
    Result,
    StatusReport,
)

VENDOR_ID = 0x1209
PRODUCT_ID = 0x0001
USAGE_PAGE = 0xFF00
USAGE = 0x01

REPORT_LEN = 1 + protocol.REPORT_PAYLOAD_LEN


class HidpinError(Exception):
    """Base class for every error raised by this library."""


class DeviceNotFound(HidpinError):
    pass


class ProtocolVersionError(HidpinError):
    def __init__(self, device_version: int) -> None:
        super().__init__(
            f"device speaks protocol version {device_version}, this library speaks {protocol.PROTOCOL_VERSION}"
        )
        self.device_version = device_version


class PinConfigRejected(HidpinError):
    def __init__(self, result: Result | int, gpio: int, *, local: bool = False) -> None:
        message = protocol.RESULT_MESSAGES.get(result, f"result {int(result)}")
        if gpio != protocol.RESULT_GPIO_NONE:
            message = f"{message} (GPIO{gpio})"
        where = "ピン設定が不正です (デバイスには送っていません)" if local else "デバイスがピン設定を拒否しました"
        super().__init__(f"{where}: {message}")
        self.result = result
        self.gpio = gpio
        self.local = local


class PinConfigConflict(HidpinError):
    """Another process changed the pin configuration at the same time (PROTOCOL.md 6.3)."""

    def __init__(self, expected: int, seen: int) -> None:
        super().__init__(f"別のプロセスがピン設定を変更しました (request_id {expected} を送り {seen} が返りました)")
        self.expected = expected
        self.seen = seen


@dataclass(frozen=True)
class DeviceEntry:
    """One hidpin device found on the host."""

    path: bytes
    serial: str
    manufacturer: str
    product: str


def _import_hid():
    try:
        import hid
    except ImportError as exc:  # pragma: no cover - depends on the host environment
        raise HidpinError(
            "hidapi が見つかりません。'pip install hidapi' を実行するか、Linux では hidraw を使ってください"
        ) from exc
    return hid


def _default_backend():
    """Linux では hidraw を使い、それ以外では hidapi を使う。

    HIDPIN_BACKEND=hidraw / hidapi で明示的に選べる。hidapi の PyPI ホイールは libusb
    バックエンドで作られており、カーネルドライバを奪えないと開けないことがあるため、
    Linux では hidraw を既定にしている。
    """
    choice = os.environ.get("HIDPIN_BACKEND", "auto").lower()
    if choice not in ("auto", "hidraw", "hidapi"):
        raise HidpinError(f"HIDPIN_BACKEND には hidraw か hidapi を指定してください: '{choice}'")
    if choice in ("auto", "hidraw") and _hidraw.available():
        return _hidraw
    if choice == "hidraw":
        raise HidpinError("hidraw が使えません (/sys/class/hidraw が見つかりません)")
    return _import_hid()


def _is_hidpin(item: dict) -> bool:
    usage_page = item.get("usage_page") or 0
    # Some backends report 0 when they cannot parse the report descriptor.
    return usage_page in (0, USAGE_PAGE)


def find_devices(backend=None) -> list[DeviceEntry]:
    """Lists connected hidpin devices."""
    hid = backend or _default_backend()
    entries = []
    for item in hid.enumerate(VENDOR_ID, PRODUCT_ID):
        if not _is_hidpin(item):
            continue
        entries.append(
            DeviceEntry(
                path=item.get("path", b""),
                serial=item.get("serial_number") or "",
                manufacturer=item.get("manufacturer_string") or "",
                product=item.get("product_string") or "",
            )
        )
    return sorted(entries, key=lambda entry: entry.serial)


class Device:
    """A connected hidpin device.

    Not thread safe: use one instance from one thread.
    """

    def __init__(self, handle, *, serial: str = "") -> None:
        self._handle = handle
        self.serial = serial
        self._info: DeviceInfo | None = None
        self._config: PinConfig | None = None
        self._polarity: dict[int, bool] = {}
        self._last_request_id = 0
        self._last_seq: int | None = None
        self.missed_reports = 0

    @classmethod
    def open(cls, serial: str | None = None, *, path: bytes | None = None, backend=None) -> "Device":
        hid = backend or _default_backend()
        if path is None:
            entries = find_devices(backend=hid)
            if serial is not None:
                entries = [entry for entry in entries if entry.serial == serial]
            if not entries:
                raise DeviceNotFound("hidpin デバイスが見つかりません" if serial is None else f"シリアル番号 {serial} のデバイスが見つかりません")
            if len(entries) > 1 and serial is None:
                found = ", ".join(entry.serial for entry in entries)
                raise DeviceNotFound(f"複数のデバイスが接続されています。--serial で指定してください: {found}")
            path = entries[0].path
            serial = entries[0].serial
        handle = hid.device()
        try:
            handle.open_path(path)
        except OSError as exc:
            raise HidpinError(
                f"デバイスを開けませんでした ({exc})。"
                "権限が足りない場合は udev ルールを入れて、ボードを挿し直してください: "
                "sudo cp udev/70-hidpin.rules /etc/udev/rules.d/ && "
                "sudo udevadm control --reload-rules && sudo udevadm trigger"
            ) from exc
        device = cls(handle, serial=serial or "")
        device.info  # verifies the protocol version before anything else
        return device

    def close(self) -> None:
        self._handle.close()

    def __enter__(self) -> "Device":
        return self

    def __exit__(self, exc_type: type[BaseException] | None, exc: BaseException | None, tb: TracebackType | None) -> None:
        self.close()

    # --- reports -----------------------------------------------------------------

    def _get_feature(self, report_id: int) -> bytes:
        data = bytes(self._handle.get_feature_report(report_id, REPORT_LEN))
        if len(data) < REPORT_LEN:
            raise HidpinError(f"Feature レポート {report_id} の長さが {len(data)} バイトしかありません")
        if data[0] != report_id:
            raise HidpinError(f"Feature レポート {report_id} を要求しましたが {data[0]} が返りました")
        return data[1:REPORT_LEN]

    def _send_feature(self, report_id: int, payload: bytes) -> None:
        self._handle.send_feature_report(bytes([report_id]) + payload)

    @property
    def info(self) -> DeviceInfo:
        """Device information; read once and cached (PROTOCOL.md 5)."""
        if self._info is None:
            info = protocol.decode_device_info(self._get_feature(protocol.REPORT_ID_DEVICE_INFO))
            if info.protocol_version != protocol.PROTOCOL_VERSION:
                raise ProtocolVersionError(info.protocol_version)
            self._info = info
        return self._info

    def get_pin_config(self) -> PinConfigReport:
        report = protocol.decode_pin_config(self._get_feature(protocol.REPORT_ID_PIN_CONFIG))
        self._config = report.config
        return report

    @property
    def pin_config(self) -> PinConfig:
        if self._config is None:
            self.get_pin_config()
        assert self._config is not None
        return self._config

    def _next_request_id(self) -> int:
        request_id = random.randint(1, 255)
        while request_id == self._last_request_id:
            request_id = random.randint(1, 255)
        self._last_request_id = request_id
        return request_id

    def set_pin_config(self, config: PinConfig, *, request_id: int | None = None) -> PinConfigReport:
        """Writes a pin configuration and verifies it was applied (PROTOCOL.md 6.3)."""
        result, gpio = protocol.validate_pin_config(config, self.info.available)
        if result != Result.OK:
            raise PinConfigRejected(result, gpio, local=True)

        request_id = request_id if request_id is not None else self._next_request_id()
        self._send_feature(protocol.REPORT_ID_PIN_CONFIG, protocol.encode_pin_config_set(config, request_id))

        report = self.get_pin_config()
        if report.request_id != request_id:
            raise PinConfigConflict(request_id, report.request_id)
        if report.result != Result.OK:
            raise PinConfigRejected(report.result, report.result_gpio)
        if report.config != config:
            raise HidpinError("デバイスが適用したピン設定が、送った内容と一致しません")
        return report

    def update_pins(self, settings: dict[int, PinSetting]) -> PinConfigReport:
        """Changes some pins, keeping every other pin as it is."""
        return self.set_pin_config(self.get_pin_config().config.with_pins(settings))

    def request_status(self) -> StatusReport:
        """Asks for the current state with Get_Report(Input) (PROTOCOL.md 4.6)."""
        data = bytes(self._handle.get_input_report(protocol.REPORT_ID_STATUS, REPORT_LEN))
        if len(data) < REPORT_LEN or data[0] != protocol.REPORT_ID_STATUS:
            raise HidpinError("状態通知の取得に失敗しました")
        return protocol.decode_status(data[1:REPORT_LEN])

    def read_status(self, timeout_ms: int = 1000) -> StatusReport | None:
        """Waits for the next status report; returns None on timeout."""
        data = self._handle.read(REPORT_LEN, timeout_ms)
        if not data:
            return None
        data = bytes(data)
        if data[0] != protocol.REPORT_ID_STATUS:
            raise HidpinError(f"予期しないレポート ID {data[0]} を受け取りました")
        report = protocol.decode_status(data[1:REPORT_LEN])
        self._track_seq(report)
        return report

    def _track_seq(self, report: StatusReport) -> None:
        if report.reason & Reason.HOST_REQUEST:
            return
        if self._last_seq is not None:
            missed = (report.seq - self._last_seq - 1) & 0xFFFF
            self.missed_reports += missed
        self._last_seq = report.seq

    # --- outputs -----------------------------------------------------------------

    def write_outputs(self, mask: int, value: int) -> None:
        payload = protocol.encode_output(mask, value)
        self._handle.write(bytes([protocol.REPORT_ID_OUTPUT]) + payload)

    def set_outputs(self, levels: dict[int, bool]) -> None:
        """Drives output pins; GPIOs that are not output pins are ignored by the device."""
        mask = 0
        value = 0
        for gpio, level in levels.items():
            mask |= 1 << gpio
            if level:
                value |= 1 << gpio
        self.write_outputs(mask, value)

    # --- polarity ----------------------------------------------------------------

    def set_polarity(self, gpio: int, active_low: bool) -> None:
        """Overrides which level counts as ON for one pin."""
        self._polarity[gpio] = active_low

    def is_active_low(self, gpio: int, config: PinConfig | None = None) -> bool:
        """LOW means ON for pins with a pull-up, unless the caller said otherwise."""
        if gpio in self._polarity:
            return self._polarity[gpio]
        config = config if config is not None else self.pin_config
        return config[gpio].mode == PinMode.PULLUP

    def on_off(self, report: StatusReport, config: PinConfig | None = None) -> dict[int, bool]:
        """Interprets the pin levels of monitored pins as ON/OFF."""
        config = config if config is not None else self.pin_config
        states = {}
        for gpio in protocol.mask_to_gpios(report.monitored):
            level = report.level(gpio)
            states[gpio] = (not level) if self.is_active_low(gpio, config) else level
        return states
