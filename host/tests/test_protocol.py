"""Checks the Python wire format against protocol/vectors.json (same data as the C tests)."""

import pytest

from hidpin import protocol


def num(value) -> int:
    return int(value, 0) if isinstance(value, str) else int(value)


def test_status_vectors(vectors):
    for case in vectors["status"]:
        payload = bytes.fromhex(case["payload"])
        report = protocol.decode_status(payload)
        assert report.seq == case["seq"], case["name"]
        assert int(report.reason) == case["reason"], case["name"]
        assert int(report.flags) == case["flags"], case["name"]
        assert report.monitored == num(case["monitored"]), case["name"]
        assert report.outputs == num(case["outputs"]), case["name"]
        assert report.levels == num(case["levels"]), case["name"]
        assert report.timestamp_us == case["timestamp_us"], case["name"]
        assert len(report.events) == len(case["events"]), case["name"]
        for event, expected in zip(report.events, case["events"]):
            assert event.gpio == expected["gpio"], case["name"]
            assert event.level == bool(expected["level"]), case["name"]
            assert event.age_us == expected["age_us"], case["name"]
        assert protocol.encode_status(report) == payload, case["name"]


def test_status_event_start_time(vectors):
    case = next(c for c in vectors["status"] if c["name"] == "single_press_event")
    report = protocol.decode_status(bytes.fromhex(case["payload"]))
    event = report.events[0]
    assert not event.saturated
    assert event.start_us(report.timestamp_us) == report.timestamp_us - event.age_us

    case = next(c for c in vectors["status"] if c["name"] == "seven_events_more_pending")
    report = protocol.decode_status(bytes.fromhex(case["payload"]))
    saturated = report.events[-1]
    assert saturated.saturated
    assert saturated.start_us(report.timestamp_us) is None


def test_device_info_vectors(vectors):
    for case in vectors["device_info"]:
        payload = bytes.fromhex(case["payload"])
        info = protocol.decode_device_info(payload)
        assert info.protocol_version == protocol.PROTOCOL_VERSION, case["name"]
        assert info.firmware == (case["fw_major"], case["fw_minor"], case["fw_patch"]), case["name"]
        assert info.board == case["board"], case["name"]
        assert info.available == num(case["available"]), case["name"]
        assert info.gpio_count == protocol.GPIO_COUNT, case["name"]
        assert info.events_per_report == protocol.EVENTS_PER_REPORT, case["name"]
        assert protocol.encode_device_info(info) == payload, case["name"]


def test_device_info_board_names(vectors):
    infos = [protocol.decode_device_info(bytes.fromhex(c["payload"])) for c in vectors["device_info"]]
    assert [info.board_name for info in infos] == ["Raspberry Pi Pico", "Adafruit QT Py RP2040"]
    assert infos[0].available_gpios == list(range(0, 23)) + [26, 27, 28]
    assert infos[1].available_gpios == [3, 4, 5, 6, 20, 22, 23, 24, 25, 26, 27, 28, 29]


def test_pin_config_vectors(vectors):
    for case in vectors["pin_config"]:
        payload = bytes.fromhex(case["payload"])
        report = protocol.decode_pin_config(payload)
        assert int(report.result) == case["result"], case["name"]
        assert report.result_gpio == case["result_gpio"], case["name"]
        assert report.request_id == case["request_id"], case["name"]
        assert [int(pin.mode) for pin in report.config.pins] == case["mode"], case["name"]
        assert [pin.param for pin in report.config.pins] == case["param"], case["name"]
        assert protocol.encode_pin_config(report) == payload, case["name"]


def test_default_pin_config_matches_vector(vectors):
    for name, available in [("pico_default", 0x1C7FFFFF), ("qtpy_rp2040_default", 0x3FD00078)]:
        case = next(c for c in vectors["pin_config"] if c["name"] == name)
        config = protocol.PinConfig.default(available)
        assert [int(pin.mode) for pin in config.pins] == case["mode"], name
        assert [pin.param for pin in config.pins] == case["param"], name


def test_pin_config_set_validation_vectors(vectors):
    for case in vectors["pin_config_set"]:
        payload = bytes.fromhex(case["payload"])
        result, gpio, request_id = protocol.validate_pin_config_payload(payload, num(case["available"]))
        assert int(result) == case["result"], case["name"]
        assert gpio == case["result_gpio"], case["name"]
        assert request_id == case["request_id"], case["name"]


def test_encode_pin_config_set_round_trip(vectors):
    case = next(c for c in vectors["pin_config_set"] if c["name"] == "valid_mixed")
    payload = bytes.fromhex(case["payload"])
    config = protocol.decode_pin_config(payload).config
    assert protocol.encode_pin_config_set(config, case["request_id"]) == payload


def test_decode_rejects_bad_payloads():
    with pytest.raises(ValueError):
        protocol.decode_status(bytes(62))
    with pytest.raises(ValueError):
        protocol.decode_device_info(bytes(64))
    with pytest.raises(ValueError):
        protocol.decode_pin_config(bytes(62))
    bad_mode = bytearray(protocol.REPORT_PAYLOAD_LEN)
    bad_mode[3] = 9
    with pytest.raises(ValueError):
        protocol.decode_pin_config(bytes(bad_mode))
    with pytest.raises(ValueError):
        protocol.encode_pin_config_set(protocol.PinConfig.default(0), 0)


def test_output_vectors(vectors):
    for case in vectors["output"]:
        payload = bytes.fromhex(case["payload"])
        if case["valid"]:
            assert protocol.decode_output(payload) == (num(case["mask"]), num(case["value"])), case["name"]
            assert protocol.encode_output(num(case["mask"]), num(case["value"])) == payload, case["name"]
        else:
            with pytest.raises(ValueError):
                protocol.decode_output(payload)


def test_pin_setting_text():
    assert str(protocol.PinSetting.unused()) == "off"
    assert str(protocol.PinSetting.monitor(protocol.PinMode.PULLUP, 20)) == "pullup:20"
    assert str(protocol.PinSetting.monitor(protocol.PinMode.INPUT, 0)) == "nopull:0"
    assert str(protocol.PinSetting.output(True)) == "out:high"
    assert protocol.PinSetting.monitor(protocol.PinMode.PULLDOWN, 5).debounce_ms == 5
    assert protocol.PinSetting.output(True).initial_level is True
    with pytest.raises(ValueError):
        protocol.PinSetting.unused().debounce_ms
    with pytest.raises(ValueError):
        protocol.PinSetting.monitor().initial_level
    with pytest.raises(ValueError):
        protocol.PinSetting.monitor(protocol.PinMode.OUTPUT)
