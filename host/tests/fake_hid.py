"""A fake hidapi handle that behaves like the firmware (docs/PROTOCOL.md)."""

from __future__ import annotations

from hidpin import protocol
from hidpin.protocol import DeviceInfo, PinConfig, PinConfigReport, Result, StatusReport

PICO_AVAILABLE = 0x1C7FFFFF


class FakeHandle:
    def __init__(self, *, available: int = PICO_AVAILABLE, protocol_version: int = protocol.PROTOCOL_VERSION) -> None:
        self.info = DeviceInfo(
            protocol_version=protocol_version,
            firmware=(0, 1, 0),
            board=protocol.Board.PICO,
            gpio_count=protocol.GPIO_COUNT,
            periodic_interval_ms=1000,
            available=available,
            events_per_report=protocol.EVENTS_PER_REPORT,
            event_queue_size=32,
        )
        self.config = PinConfig.default(available)
        self.result: Result = Result.OK
        self.result_gpio = protocol.RESULT_GPIO_NONE
        self.request_id = 0
        # Lets a test pretend another process wrote a configuration in between.
        self.force_request_id: int | None = None
        # Lets a test make the device reject what the host considers valid.
        self.validate_available: int | None = None
        self.reads: list[bytes] = []
        self.feature_writes: list[bytes] = []
        self.output_writes: list[bytes] = []
        self.closed = False

    # --- hidapi API ---------------------------------------------------------

    def get_feature_report(self, report_id: int, length: int) -> list[int]:
        if report_id == protocol.REPORT_ID_DEVICE_INFO:
            payload = protocol.encode_device_info(self.info)
        elif report_id == protocol.REPORT_ID_PIN_CONFIG:
            payload = protocol.encode_pin_config(
                PinConfigReport(self.result, self.result_gpio, self.request_id, self.config)
            )
        else:
            raise AssertionError(f"unexpected feature report {report_id}")
        return list(bytes([report_id]) + payload)[:length]

    def send_feature_report(self, data) -> int:
        data = bytes(data)
        self.feature_writes.append(data)
        assert data[0] == protocol.REPORT_ID_PIN_CONFIG
        payload = data[1:]
        available = self.validate_available if self.validate_available is not None else self.info.available
        result, gpio, request_id = protocol.validate_pin_config_payload(payload, available)
        self.result, self.result_gpio = result, gpio
        self.request_id = self.force_request_id if self.force_request_id is not None else request_id
        if result == Result.OK:
            self.config = protocol.decode_pin_config(payload).config
        return len(data)

    def get_input_report(self, report_id: int, length: int) -> list[int]:
        assert report_id == protocol.REPORT_ID_STATUS
        return list(bytes([report_id]) + protocol.encode_status(self.current_status()))[:length]

    def read(self, length: int, timeout_ms: int = 0) -> list[int]:
        if not self.reads:
            return []
        return list(self.reads.pop(0))[:length]

    def write(self, data) -> int:
        data = bytes(data)
        self.output_writes.append(data)
        return len(data)

    def close(self) -> None:
        self.closed = True

    # --- helpers for tests ---------------------------------------------------

    def current_status(self, **overrides) -> StatusReport:
        fields = {
            "seq": 0,
            "reason": protocol.Reason.HOST_REQUEST,
            "flags": protocol.StatusFlags(0),
            "monitored": self.config.monitored_mask,
            "outputs": self.config.outputs_mask,
            "levels": self.config.monitored_mask,
            "timestamp_us": 1_000_000,
            "events": (),
        }
        fields.update(overrides)
        return StatusReport(**fields)

    def queue_status(self, report: StatusReport) -> None:
        self.reads.append(bytes([protocol.REPORT_ID_STATUS]) + protocol.encode_status(report))
