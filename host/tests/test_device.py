"""Device-level behaviour against a fake hidapi handle."""

import pytest
from fake_hid import PICO_AVAILABLE, FakeHandle

from hidpin import device as device_module, protocol
from hidpin.device import Device, HidpinError, PinConfigConflict, PinConfigRejected, ProtocolVersionError
from hidpin.protocol import PinMode, PinSetting, Reason, Result, StatusFlags


def make_device(**kwargs) -> tuple[Device, FakeHandle]:
    handle = FakeHandle(**kwargs)
    return Device(handle, serial="ABCD0123456789EF"), handle


def test_info_is_read_once():
    device, handle = make_device()
    assert device.info.board_name == "Raspberry Pi Pico"
    assert device.info.available == PICO_AVAILABLE
    assert device.info is device.info


def test_unsupported_protocol_version():
    device, _ = make_device(protocol_version=2)
    with pytest.raises(ProtocolVersionError):
        _ = device.info


def test_set_pin_config_applies_and_verifies():
    device, handle = make_device()
    report = device.update_pins({5: PinSetting.monitor(PinMode.PULLDOWN, 5), 7: PinSetting.output(True)})

    assert report.result == Result.OK
    assert handle.config[5] == PinSetting(PinMode.PULLDOWN, 5)
    assert handle.config[7] == PinSetting(PinMode.OUTPUT, 1)
    assert handle.config[6] == PinSetting(PinMode.PULLUP, protocol.DEFAULT_DEBOUNCE_MS)
    sent = handle.feature_writes[-1]
    assert sent[0] == protocol.REPORT_ID_PIN_CONFIG and len(sent) == 64
    assert 1 <= sent[3] <= 255  # request_id: payload offset 2, i.e. index 3 with the report ID
    assert report.request_id == sent[3]


def test_set_pin_config_detects_conflict():
    device, handle = make_device()
    handle.force_request_id = 99
    with pytest.raises(PinConfigConflict) as error:
        device.update_pins({5: PinSetting.unused()})
    assert error.value.seen == 99


def test_set_pin_config_rejected_by_device():
    device, handle = make_device()
    handle.validate_available = 0  # the device considers every GPIO unavailable
    with pytest.raises(PinConfigRejected) as error:
        device.update_pins({5: PinSetting.monitor()})
    assert error.value.result == Result.UNAVAILABLE_GPIO
    assert error.value.gpio == 0


def test_local_validation_happens_before_writing():
    device, handle = make_device()
    with pytest.raises(PinConfigRejected) as error:
        device.update_pins({23: PinSetting.monitor()})  # GPIO23 is not available on the Pico
    assert error.value.gpio == 23
    assert handle.feature_writes == []


def test_applied_config_must_match():
    device, handle = make_device()

    original = handle.send_feature_report

    def drop_one_pin(data):
        result = original(data)
        handle.config = handle.config.with_pin(9, PinSetting.unused())
        return result

    handle.send_feature_report = drop_one_pin
    with pytest.raises(HidpinError, match="differs"):
        device.update_pins({5: PinSetting.unused()})


def test_read_status_counts_missed_reports():
    device, handle = make_device()
    handle.queue_status(handle.current_status(seq=10, reason=Reason.PERIODIC))
    handle.queue_status(handle.current_status(seq=14, reason=Reason.PERIODIC))
    handle.queue_status(handle.current_status(seq=15, reason=Reason.PERIODIC))

    assert device.read_status().seq == 10
    assert device.missed_reports == 0
    assert device.read_status().seq == 14
    assert device.missed_reports == 3
    assert device.read_status().seq == 15
    assert device.missed_reports == 3
    assert device.read_status() is None


def test_seq_wraps_without_false_gaps():
    device, handle = make_device()
    handle.queue_status(handle.current_status(seq=65535, reason=Reason.PERIODIC))
    handle.queue_status(handle.current_status(seq=0, reason=Reason.PERIODIC))
    device.read_status()
    device.read_status()
    assert device.missed_reports == 0


def test_host_request_does_not_affect_seq_tracking():
    device, handle = make_device()
    handle.queue_status(handle.current_status(seq=5, reason=Reason.PERIODIC))
    handle.queue_status(handle.current_status(seq=5, reason=Reason.HOST_REQUEST))
    handle.queue_status(handle.current_status(seq=6, reason=Reason.PERIODIC))
    for _ in range(3):
        device.read_status()
    assert device.missed_reports == 0


def test_request_status_reads_current_state():
    device, handle = make_device()
    report = device.request_status()
    assert report.reason & Reason.HOST_REQUEST
    assert report.monitored == handle.config.monitored_mask


def test_on_off_uses_pull_direction():
    device, handle = make_device()
    device.update_pins({6: PinSetting.monitor(PinMode.PULLDOWN, 0)})
    levels = handle.config.monitored_mask & ~(1 << 5)  # GPIO5 pulled low by a switch
    report = handle.current_status(levels=levels)

    states = device.on_off(report)
    assert states[5] is True  # pull-up pin: LOW means ON
    assert states[4] is False
    assert states[6] is True  # pull-down pin: HIGH means ON

    device.set_polarity(5, active_low=False)
    assert device.on_off(report)[5] is False


def test_set_outputs_encodes_mask_and_value():
    device, handle = make_device()
    device.update_pins({7: PinSetting.output(False), 8: PinSetting.output(False)})
    device.set_outputs({7: True, 8: False})

    written = handle.output_writes[-1]
    assert written[0] == protocol.REPORT_ID_OUTPUT
    mask, value = protocol.decode_output(written[1:])
    assert mask == (1 << 7) | (1 << 8)
    assert value == 1 << 7


def test_status_flags_are_exposed():
    device, handle = make_device()
    handle.queue_status(
        handle.current_status(
            seq=1,
            reason=Reason.LEVEL_CHANGED,
            flags=StatusFlags.OVERFLOW | StatusFlags.MORE_EVENTS,
            events=(protocol.EdgeEvent(gpio=5, level=False, age_us=20_000),),
        )
    )
    report = device.read_status()
    assert report.overflow and report.more_events
    assert report.events[0].gpio == 5


class RecordingBackend:
    """A backend that only records the ids find_devices() filters by."""

    def __init__(self):
        self.calls = []

    def enumerate(self, vendor_id=0, product_id=0):
        self.calls.append((vendor_id, product_id))
        return []


def test_usb_ids_default(monkeypatch):
    monkeypatch.delenv("HIDPIN_VID", raising=False)
    monkeypatch.delenv("HIDPIN_PID", raising=False)
    assert device_module.usb_ids() == (0x1209, 0x0001)


def test_usb_ids_from_environment(monkeypatch):
    monkeypatch.setenv("HIDPIN_PID", "0x1234")
    assert device_module.usb_ids() == (0x1209, 0x1234)
    monkeypatch.setenv("HIDPIN_VID", "4660")  # decimal is accepted too
    assert device_module.usb_ids() == (4660, 0x1234)
    monkeypatch.setenv("HIDPIN_PID", "")
    assert device_module.usb_ids()[1] == 0x0001


@pytest.mark.parametrize("value", ["zz", "0x10000", "-1"])
def test_usb_ids_rejects_bad_values(monkeypatch, value):
    monkeypatch.setenv("HIDPIN_PID", value)
    with pytest.raises(HidpinError, match="HIDPIN_PID"):
        device_module.usb_ids()


def test_find_devices_uses_overridden_ids(monkeypatch):
    monkeypatch.setenv("HIDPIN_VID", "0x2E8A")
    monkeypatch.setenv("HIDPIN_PID", "0x10AB")
    backend = RecordingBackend()
    assert device_module.find_devices(backend=backend) == []
    assert backend.calls == [(0x2E8A, 0x10AB)]


def test_close_closes_handle():
    device, handle = make_device()
    with device:
        pass
    assert handle.closed
