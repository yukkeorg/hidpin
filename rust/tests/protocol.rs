//! Checks the wire format against protocol/vectors.json (the same data as the C, Python and Go
//! tests), and the types of docs/HOST_API.md 4.

use hidpin::protocol::{self, board_name, mask_to_gpios, RESULT_GPIO_NONE};
use hidpin::{Board, Chip, EdgeEvent, PinConfig, PinMode, PinSetting, Reason, ResultCode, StatusFlags, StatusReport};
use serde_json::Value;

fn vectors() -> Value {
    let text = std::fs::read_to_string(concat!(env!("CARGO_MANIFEST_DIR"), "/../protocol/vectors.json")).unwrap();
    serde_json::from_str(&text).unwrap()
}

/// A number in the vectors: an integer or a "0x..." string.
fn num(value: &Value) -> u64 {
    match value {
        Value::Number(n) => n.as_u64().unwrap(),
        Value::String(s) => u64::from_str_radix(s.trim_start_matches("0x"), 16).unwrap(),
        other => panic!("not a number: {other}"),
    }
}

fn unhex(value: &Value) -> Vec<u8> {
    let s = value.as_str().unwrap();
    (0..s.len())
        .step_by(2)
        .map(|i| u8::from_str_radix(&s[i..i + 2], 16).unwrap())
        .collect()
}

fn cases<'a>(vectors: &'a Value, kind: &str) -> &'a Vec<Value> {
    vectors[kind].as_array().unwrap()
}

#[test]
fn status_vectors() {
    let vectors = vectors();
    for case in cases(&vectors, "status") {
        let name = case["name"].as_str().unwrap();
        let payload = unhex(&case["payload"]);
        let report = protocol::decode_status(&payload).unwrap();
        assert_eq!(u64::from(report.seq), num(&case["seq"]), "{name}");
        assert_eq!(u64::from(report.reason.0), num(&case["reason"]), "{name}");
        assert_eq!(u64::from(report.flags.0), num(&case["flags"]), "{name}");
        assert_eq!(u64::from(report.monitored), num(&case["monitored"]), "{name}");
        assert_eq!(u64::from(report.outputs), num(&case["outputs"]), "{name}");
        assert_eq!(u64::from(report.levels), num(&case["levels"]), "{name}");
        assert_eq!(report.timestamp_us, num(&case["timestamp_us"]), "{name}");
        let events: Vec<EdgeEvent> = case["events"]
            .as_array()
            .unwrap()
            .iter()
            .map(|e| EdgeEvent {
                gpio: num(&e["gpio"]) as u8,
                level: num(&e["level"]) != 0,
                age_us: num(&e["age_us"]) as u32,
            })
            .collect();
        assert_eq!(report.events, events, "{name}");
        assert_eq!(report.encode().unwrap(), payload, "{name}");
    }
}

#[test]
fn device_info_vectors() {
    let vectors = vectors();
    let mut names = Vec::new();
    for case in cases(&vectors, "device_info") {
        let name = case["name"].as_str().unwrap();
        let payload = unhex(&case["payload"]);
        let info = protocol::decode_device_info(&payload).unwrap();
        assert_eq!(info.protocol_version, protocol::PROTOCOL_VERSION, "{name}");
        let firmware = [
            num(&case["fw_major"]) as u8,
            num(&case["fw_minor"]) as u8,
            num(&case["fw_patch"]) as u8,
        ];
        assert_eq!(info.firmware, firmware, "{name}");
        assert_eq!(u64::from(info.board.0), num(&case["board"]), "{name}");
        assert_eq!(u64::from(info.available), num(&case["available"]), "{name}");
        assert_eq!(usize::from(info.gpio_count), protocol::GPIO_COUNT, "{name}");
        assert_eq!(
            usize::from(info.events_per_report),
            protocol::EVENTS_PER_REPORT,
            "{name}"
        );
        assert_eq!(u64::from(info.chip.0), num(&case["chip"]), "{name}");
        assert_eq!(u64::from(info.chip_revision), num(&case["chip_revision"]), "{name}");
        assert_eq!(u64::from(info.quirks.0), num(&case["quirks"]), "{name}");
        assert_eq!(info.sdk_board, case["sdk_board"].as_str().unwrap(), "{name}");
        assert_eq!(info.encode(), payload, "{name}");
        names.push((info.board_name(), info.chip_name(), info.pull_down_unreliable()));
    }
    assert_eq!(
        names,
        [
            ("Raspberry Pi Pico".into(), "not reported".into(), false),
            ("Adafruit QT Py RP2040".into(), "RP2040".into(), false),
            ("Raspberry Pi Pico 2".into(), "RP2350A".into(), true),
            ("pico_w".into(), "RP2040".into(), false),
            (
                "a_board_name_of_forty_characters_exactly".into(),
                "RP2350A".into(),
                false
            ),
        ]
    );
}

#[test]
fn sdk_board_is_ascii() {
    let mut payload = hidpin::DeviceInfo {
        sdk_board: "pico".into(),
        ..Default::default()
    }
    .encode();
    payload[18] = 0xC3;
    assert_eq!(protocol::decode_device_info(&payload).unwrap().sdk_board, "p\u{FFFD}co");
}

#[test]
fn pin_config_vectors() {
    let vectors = vectors();
    for case in cases(&vectors, "pin_config") {
        let name = case["name"].as_str().unwrap();
        let payload = unhex(&case["payload"]);
        let report = protocol::decode_pin_config(&payload).unwrap();
        assert_eq!(u64::from(report.result.0), num(&case["result"]), "{name}");
        assert_eq!(u64::from(report.result_gpio), num(&case["result_gpio"]), "{name}");
        assert_eq!(u64::from(report.request_id), num(&case["request_id"]), "{name}");
        for gpio in 0..protocol::GPIO_COUNT {
            assert_eq!(
                u64::from(report.config.0[gpio].mode as u8),
                num(&case["mode"][gpio]),
                "{name} GPIO{gpio}"
            );
            assert_eq!(
                u64::from(report.config.0[gpio].param),
                num(&case["param"][gpio]),
                "{name} GPIO{gpio}"
            );
        }
        assert_eq!(report.encode(), payload, "{name}");
    }
}

#[test]
fn default_pin_config_matches_vectors() {
    let vectors = vectors();
    for (name, available) in [("pico_default", 0x1C7F_FFFF), ("qtpy_rp2040_default", 0x3FD0_0078)] {
        let case = cases(&vectors, "pin_config")
            .iter()
            .find(|c| c["name"] == name)
            .unwrap();
        let report = protocol::decode_pin_config(&unhex(&case["payload"])).unwrap();
        assert_eq!(PinConfig::default_for(available), report.config, "{name}");
    }
}

#[test]
fn pin_config_set_validation_vectors() {
    let vectors = vectors();
    for case in cases(&vectors, "pin_config_set") {
        let name = case["name"].as_str().unwrap();
        let (result, gpio, request_id) =
            protocol::validate_pin_config_payload(&unhex(&case["payload"]), num(&case["available"]) as u32);
        assert_eq!(u64::from(result.0), num(&case["result"]), "{name}");
        assert_eq!(u64::from(gpio), num(&case["result_gpio"]), "{name}");
        assert_eq!(u64::from(request_id), num(&case["request_id"]), "{name}");
    }
}

#[test]
fn output_vectors() {
    let vectors = vectors();
    for case in cases(&vectors, "output") {
        let name = case["name"].as_str().unwrap();
        let payload = unhex(&case["payload"]);
        if case["valid"].as_bool().unwrap() {
            let (mask, value) = (num(&case["mask"]) as u32, num(&case["value"]) as u32);
            assert_eq!(protocol::decode_output(&payload).unwrap(), (mask, value), "{name}");
            assert_eq!(protocol::encode_output(mask, value), payload, "{name}");
        } else {
            assert!(protocol::decode_output(&payload).is_err(), "{name}");
        }
    }
}

#[test]
fn report_descriptor_vector_is_the_hidpin_collection() {
    let vectors = vectors();
    let descriptor = unhex(&vectors["report_descriptor"]);
    assert_eq!(&descriptor[..7], &[0x06, 0x00, 0xFF, 0x09, 0x01, 0xA1, 0x01]);
}

#[test]
fn decoders_reject_bad_payloads() {
    assert!(protocol::decode_status(&[0; 62]).is_err());
    assert!(protocol::decode_device_info(&[0; 64]).is_err());
    assert!(protocol::decode_pin_config(&[0; 62]).is_err());
    let mut bad_mode = [0u8; 63];
    bad_mode[3] = 9;
    assert_eq!(
        protocol::decode_pin_config(&bad_mode).unwrap_err().to_string(),
        "mode 9 of GPIO0 is undefined"
    );
    let mut too_many = [0u8; 63];
    too_many[4] = 8;
    assert_eq!(
        protocol::decode_status(&too_many).unwrap_err().to_string(),
        "event_count 8 exceeds 7"
    );
    assert!(protocol::encode_pin_config_set(&PinConfig::unused(), 0).is_err());
}

#[test]
fn encode_status_rejects_more_than_seven_events() {
    let event = EdgeEvent {
        gpio: 5,
        level: true,
        age_us: 0,
    };
    let report = StatusReport {
        events: vec![event; 8],
        ..Default::default()
    };
    assert!(report.encode().is_err());
}

#[test]
fn edge_event_start_time() {
    let event = EdgeEvent {
        gpio: 5,
        level: false,
        age_us: 20_000,
    };
    assert_eq!(event.start_us(4_000_000), Some(3_980_000));
    let old = EdgeEvent {
        age_us: protocol::SATURATED_AGE_US,
        ..event
    };
    assert!(old.saturated());
    assert_eq!(old.start_us(4_000_000), None);
}

#[test]
fn status_report_accessors() {
    let report = StatusReport {
        flags: StatusFlags::OVERFLOW,
        monitored: 1 << 5,
        outputs: 1 << 7,
        levels: 1 << 7,
        ..Default::default()
    };
    assert!(report.overflow() && !report.more_events());
    assert_eq!(
        (report.is_monitored(5), report.is_output(5), report.level(5)),
        (true, false, false)
    );
    assert_eq!(
        (report.is_monitored(7), report.is_output(7), report.level(7)),
        (false, true, true)
    );
}

#[test]
fn flags_contain_and_print() {
    let reason = Reason::LEVEL_CHANGED | Reason::PERIODIC;
    assert!(reason.contains(Reason::PERIODIC) && !reason.contains(Reason::HOST_REQUEST));
    assert_eq!(reason.to_string(), "LEVEL_CHANGED|PERIODIC");
    assert_eq!(Reason(0).to_string(), "0");
    assert_eq!(
        (StatusFlags::OVERFLOW | StatusFlags::MORE_EVENTS).names(),
        ["OVERFLOW", "MORE_EVENTS"]
    );
    assert_eq!(Reason(0x40).to_string(), "0", "unknown bits are kept but have no name");
}

#[test]
fn result_messages() {
    let messages: Vec<String> = (0..=5).map(|r| ResultCode(r).message()).collect();
    assert_eq!(
        messages,
        [
            "applied",
            "the payload is not 63 bytes long",
            "mode is outside 0-4",
            "the pin is not an available GPIO",
            "an unused pin was given a parameter",
            "the initial output level is neither 0 nor 1",
        ]
    );
    assert_eq!(ResultCode(9).message(), "result 9");
}

#[test]
fn board_and_chip_names() {
    assert_eq!(board_name(Board::PICO, "pico"), "Raspberry Pi Pico");
    assert_eq!(board_name(Board::OTHER, "pico_w"), "pico_w");
    assert_eq!(board_name(Board::OTHER, ""), "other board");
    assert_eq!(board_name(Board(9), ""), "unknown board 9");
    assert_eq!(board_name(Board(9), "future_board"), "future_board");
    assert_eq!(Chip(0).name(), "not reported");
    assert_eq!(Chip(7).name(), "unknown chip 7");
}

#[test]
fn pin_settings() {
    assert!(PinMode::PullDown.is_input() && !PinMode::Output.is_input() && !PinMode::Unused.is_input());
    assert_eq!(
        PinSetting::monitor(PinMode::PullUp, 20).unwrap(),
        PinSetting::pull_up(20)
    );
    assert!(PinSetting::monitor(PinMode::Output, 0).is_err());
    let texts: Vec<String> = [
        PinSetting::unused(),
        PinSetting::no_pull(0),
        PinSetting::pull_up(20),
        PinSetting::pull_down(255),
        PinSetting::output(false),
        PinSetting::output(true),
    ]
    .iter()
    .map(ToString::to_string)
    .collect();
    assert_eq!(
        texts,
        ["off", "nopull:0", "pullup:20", "pulldown:255", "out:low", "out:high"]
    );
    assert!(PinSetting::output(true).is_output() && !PinSetting::output(true).is_monitored());
}

#[test]
fn pin_configs() {
    let config = PinConfig::unused()
        .with_pins([
            (5, PinSetting::pull_up(20)),
            (6, PinSetting::no_pull(0)),
            (7, PinSetting::output(false)),
        ])
        .unwrap();
    assert_eq!((config.monitored_mask(), config.outputs_mask()), (0x60, 0x80));
    assert_eq!(config[5], PinSetting::pull_up(20));
    assert_eq!(
        PinConfig::unused(),
        PinConfig::default(),
        "with_pins leaves the original alone"
    );
    assert_eq!(
        config.with_pin(30, PinSetting::unused()).unwrap_err().to_string(),
        "GPIO30 is out of range (0-29)"
    );
    let pull_downs = PinConfig::unused()
        .with_pins([(9, PinSetting::pull_down(20)), (2, PinSetting::pull_down(20))])
        .unwrap();
    assert_eq!(pull_downs.pull_down_gpios(), [2, 9]);
}

#[test]
fn local_validation() {
    let bad = PinConfig::unused()
        .with_pin(
            3,
            PinSetting {
                mode: PinMode::Unused,
                param: 1,
            },
        )
        .unwrap();
    assert_eq!(
        protocol::validate_pin_config(&bad, 0x1C7F_FFFF),
        (ResultCode::UNUSED_WITH_PARAM, 3)
    );
    let unavailable = PinConfig::unused().with_pin(23, PinSetting::pull_up(20)).unwrap();
    assert_eq!(
        protocol::validate_pin_config(&unavailable, 0x1C7F_FFFF),
        (ResultCode::UNAVAILABLE_GPIO, 23)
    );
    assert_eq!(
        protocol::validate_pin_config(&PinConfig::default_for(0x1C7F_FFFF), 0x1C7F_FFFF),
        (ResultCode::OK, RESULT_GPIO_NONE)
    );
    assert_eq!(mask_to_gpios(0x60), [5, 6]);
}
