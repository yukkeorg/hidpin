"""Checks the Python wire format against protocol/vectors.json (same data as the C tests)."""

import dataclasses

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
        assert (info.chip, info.chip_revision, info.quirks) == (
            case["chip"],
            case["chip_revision"],
            case["quirks"],
        ), case["name"]
        assert info.sdk_board == case["sdk_board"], case["name"]
        assert protocol.encode_device_info(info) == payload, case["name"]


def test_device_info_board_names(vectors):
    infos = [protocol.decode_device_info(bytes.fromhex(c["payload"])) for c in vectors["device_info"]]
    assert [info.board_name for info in infos] == [
        "Raspberry Pi Pico",
        "Adafruit QT Py RP2040",
        "Raspberry Pi Pico 2",
        "pico_w",
        "a_board_name_of_forty_characters_exactly",
    ]
    assert [info.chip_name for info in infos] == ["not reported", "RP2040", "RP2350A", "RP2040", "RP2350A"]
    assert [info.pull_down_unreliable for info in infos] == [False, False, True, False, False]
    assert infos[0].available_gpios == list(range(0, 23)) + [26, 27, 28]
    assert infos[1].available_gpios == [3, 4, 5, 6, 20, 22, 23, 24, 25, 26, 27, 28, 29]




def test_result_message():
    assert protocol.result_message(protocol.Result.UNAVAILABLE_GPIO) == "the pin is not an available GPIO"
    assert protocol.result_message(9) == "result 9"

def test_board_name_without_a_preset():
    assert protocol.board_name(0) == "other board"
    assert protocol.board_name(9) == "unknown board 9"
    assert protocol.board_name(9, "future_board") == "future_board"
    assert protocol.board_name(1, "pico") == "Raspberry Pi Pico"


def test_sdk_board_too_long(vectors):
    info = protocol.decode_device_info(bytes.fromhex(vectors["device_info"][0]["payload"]))
    with pytest.raises(ValueError):
        protocol.encode_device_info(dataclasses.replace(info, sdk_board="x" * (protocol.SDK_BOARD_LEN + 1)))

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


def test_decode_status_rejects_more_than_seven_events():
    payload = bytearray(protocol.REPORT_PAYLOAD_LEN)
    payload[4] = protocol.EVENTS_PER_REPORT + 1
    with pytest.raises(ValueError, match="event_count 8 exceeds 7"):
        protocol.decode_status(bytes(payload))


def test_encode_status_rejects_more_than_seven_events():
    event = protocol.EdgeEvent(gpio=5, level=True, age_us=0)
    report = protocol.StatusReport(0, protocol.Reason.PERIODIC, protocol.StatusFlags(0), 0, 0, 0, 0, (event,) * 8)
    with pytest.raises(ValueError):
        protocol.encode_status(report)


def test_status_report_bit_accessors():
    report = protocol.StatusReport(
        0, protocol.Reason.PERIODIC, protocol.StatusFlags(0), monitored=1 << 5, outputs=1 << 7, levels=1 << 7, timestamp_us=0
    )
    assert (report.is_monitored(5), report.is_output(5), report.level(5)) == (True, False, False)
    assert (report.is_monitored(7), report.is_output(7), report.level(7)) == (False, True, True)


def test_unknown_values_are_kept():
    payload = bytearray(protocol.encode_pin_config(protocol.decode_pin_config(bytes(protocol.REPORT_PAYLOAD_LEN))))
    payload[0] = 9  # a result this library does not know
    assert protocol.decode_pin_config(bytes(payload)).result == 9
    assert protocol.chip_name(7) == "unknown chip 7"
    assert protocol.chip_name(0) == "not reported"


def test_pin_config_needs_every_gpio():
    with pytest.raises(ValueError, match="needs 30 entries, got 29"):
        protocol.PinConfig((protocol.PinSetting.unused(),) * 29)


@pytest.mark.parametrize("gpio", [-1, 30])
def test_with_pin_rejects_gpios_out_of_range(gpio):
    with pytest.raises(ValueError, match=f"GPIO{gpio} is out of range"):
        protocol.PinConfig.unused().with_pin(gpio, protocol.PinSetting.unused())


def test_with_pins_keeps_the_original():
    original = protocol.PinConfig.unused()
    changed = original.with_pins({0: protocol.PinSetting.output(True), 29: protocol.PinSetting.monitor()})
    assert original == protocol.PinConfig.unused()
    assert (changed[0], changed[29]) == (protocol.PinSetting.output(True), protocol.PinSetting.monitor())
    assert changed.outputs_mask == 1 and changed.monitored_mask == 1 << 29


def test_pin_setting_constructors_and_accessors():
    assert protocol.PinSetting.monitor(protocol.PinMode.PULLDOWN, 255).debounce_ms == 255
    assert protocol.PinSetting.output(True).initial_level is True
    with pytest.raises(ValueError, match="not an input mode"):
        protocol.PinSetting.monitor(protocol.PinMode.OUTPUT)
    with pytest.raises(ValueError):
        _ = protocol.PinSetting.output(False).debounce_ms
    with pytest.raises(ValueError):
        _ = protocol.PinSetting.monitor().initial_level


def test_pull_down_gpios_are_in_order():
    config = protocol.PinConfig.unused().with_pins(
        {9: protocol.PinSetting.monitor(protocol.PinMode.PULLDOWN), 2: protocol.PinSetting.monitor(protocol.PinMode.PULLDOWN)}
    )
    assert config.pull_down_gpios == [2, 9]
