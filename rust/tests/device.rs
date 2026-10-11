//! `Device` against a fake transport (docs/HOST_API.md 6-8).

use std::collections::VecDeque;
use std::io;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use hidpin::protocol::{self, REPORT_ID_OUTPUT, REPORT_ID_PIN_CONFIG, REPORT_ID_STATUS, RESULT_GPIO_NONE};
use hidpin::testing::PICO_AVAILABLE;
use hidpin::{
    choose_device, Board, Device, DeviceEntry, DeviceInfo, Error, PinConfig, PinConfigReport, PinSetting, Reason,
    ResultCode, StatusReport, Transport,
};

/// Replaces the answer to a feature read.
type FeatureAnswer = Box<dyn FnMut(&mut [u8]) -> io::Result<usize> + Send>;

/// What the fake device holds; the test keeps a handle to it while the `Device` owns the transport.
struct State {
    info: DeviceInfo,
    config: PinConfig,
    result: ResultCode,
    result_gpio: u8,
    request_id: u8,
    /// Read back a request_id that differs from the one written, as if another process wrote.
    conflict: bool,
    /// Validate against this mask instead of the available GPIOs.
    validate_available: Option<u32>,
    /// Apply the configuration with GPIO5 unused, whatever was written.
    drop_gpio5: bool,
    reads: VecDeque<Vec<u8>>,
    feature_reads: Vec<u8>,
    feature_writes: Vec<Vec<u8>>,
    output_writes: Vec<Vec<u8>>,
    /// Replaces the answer to a feature read.
    feature_override: Option<FeatureAnswer>,
    /// Replaces the answer to Get_Report(Input).
    input_override: Option<usize>,
}

#[derive(Clone)]
struct Fake(Arc<Mutex<State>>);

impl Fake {
    fn new(protocol_version: u8) -> Fake {
        Fake(Arc::new(Mutex::new(State {
            info: DeviceInfo {
                protocol_version,
                firmware: [0, 1, 0],
                board: Board::PICO,
                gpio_count: 30,
                periodic_interval_ms: 1000,
                available: PICO_AVAILABLE,
                events_per_report: 7,
                event_queue_size: 32,
                ..DeviceInfo::default()
            },
            config: PinConfig::default_for(PICO_AVAILABLE),
            result: ResultCode::OK,
            result_gpio: RESULT_GPIO_NONE,
            request_id: 0,
            conflict: false,
            validate_available: None,
            drop_gpio5: false,
            reads: VecDeque::new(),
            feature_reads: Vec::new(),
            feature_writes: Vec::new(),
            output_writes: Vec::new(),
            feature_override: None,
            input_override: None,
        })))
    }

    fn state(&self) -> std::sync::MutexGuard<'_, State> {
        self.0.lock().unwrap()
    }

    fn queue(&self, report: StatusReport) {
        let mut data = vec![REPORT_ID_STATUS];
        data.extend(report.encode().unwrap());
        self.state().reads.push_back(data);
    }

    fn current_status(&self, seq: u16, reason: Reason) -> StatusReport {
        let s = self.state();
        StatusReport {
            seq,
            reason,
            monitored: s.config.monitored_mask(),
            outputs: s.config.outputs_mask(),
            levels: s.config.monitored_mask(),
            timestamp_us: 1_000_000,
            ..Default::default()
        }
    }
}

impl Transport for Fake {
    fn read(&mut self, buf: &mut [u8], _timeout: Option<Duration>) -> io::Result<usize> {
        match self.state().reads.pop_front() {
            None => Ok(0),
            Some(report) => {
                buf[..report.len()].copy_from_slice(&report);
                Ok(report.len())
            }
        }
    }

    fn write(&mut self, report: &[u8]) -> io::Result<usize> {
        self.state().output_writes.push(report.to_vec());
        Ok(report.len())
    }

    fn get_feature_report(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        let mut s = self.state();
        s.feature_reads.push(buf[0]);
        if let Some(answer) = s.feature_override.as_mut() {
            return answer(buf);
        }
        let payload = match buf[0] {
            protocol::REPORT_ID_DEVICE_INFO => s.info.encode(),
            REPORT_ID_PIN_CONFIG => PinConfigReport {
                result: s.result,
                result_gpio: s.result_gpio,
                request_id: s.request_id,
                config: s.config,
            }
            .encode(),
            other => panic!("unexpected feature report {other}"),
        };
        buf[1..64].copy_from_slice(&payload);
        Ok(64)
    }

    fn send_feature_report(&mut self, report: &[u8]) -> io::Result<usize> {
        let mut s = self.state();
        s.feature_writes.push(report.to_vec());
        let available = s.validate_available.unwrap_or(s.info.available);
        let (result, gpio, request_id) = protocol::validate_pin_config_payload(&report[1..], available);
        s.result = result;
        s.result_gpio = gpio;
        s.request_id = if s.conflict { request_id % 255 + 1 } else { request_id };
        if result == ResultCode::OK {
            let mut config = protocol::decode_pin_config(&report[1..]).unwrap().config;
            if s.drop_gpio5 {
                config = config.with_pin(5, PinSetting::unused()).unwrap();
            }
            s.config = config;
        }
        Ok(report.len())
    }

    fn get_input_report(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        if let Some(n) = self.state().input_override {
            return Ok(n);
        }
        let payload = self.current_status(0, Reason::HOST_REQUEST).encode().unwrap();
        buf[0] = REPORT_ID_STATUS;
        buf[1..64].copy_from_slice(&payload);
        Ok(64)
    }
}

fn device() -> (Device, Fake) {
    let fake = Fake::new(1);
    (Device::new(Box::new(fake.clone()), "ABCD0123456789EF"), fake)
}

// --- information and configuration (7.1-7.3) --------------------------------------------------

#[test]
fn info_is_read_once_and_version_checked() {
    let (mut device, fake) = device();
    let info = device.info().unwrap();
    assert_eq!(
        (info.board_name(), info.available),
        ("Raspberry Pi Pico".to_string(), PICO_AVAILABLE)
    );
    device.info().unwrap();
    assert_eq!(fake.state().feature_reads, [protocol::REPORT_ID_DEVICE_INFO]);
    let mut old = Device::new(Box::new(Fake::new(2)), "");
    assert!(matches!(old.info(), Err(Error::ProtocolVersion { device_version: 2 })));
}

#[test]
fn set_pin_config_applies_and_verifies() {
    let (mut device, fake) = device();
    let report = device
        .update_pins([(5, PinSetting::pull_down(5)), (7, PinSetting::output(true))])
        .unwrap();
    assert_eq!(report.result, ResultCode::OK);
    assert_eq!(fake.state().config[5], PinSetting::pull_down(5));
    assert_eq!(fake.state().config[7], PinSetting::output(true));
    assert_eq!(device.pin_config().unwrap(), fake.state().config);
}

#[test]
fn set_pin_config_detects_a_conflict() {
    let (mut device, fake) = device();
    fake.state().conflict = true;
    assert!(matches!(
        device.update_pins([(5, PinSetting::unused())]),
        Err(Error::PinConfigConflict { .. })
    ));
}

#[test]
fn set_pin_config_rejected_by_the_device() {
    let (mut device, fake) = device();
    fake.state().validate_available = Some(0);
    let error = device.update_pins([(5, PinSetting::pull_up(20))]).unwrap_err();
    assert!(matches!(
        error,
        Error::PinConfigRejected {
            result: ResultCode::UNAVAILABLE_GPIO,
            gpio: 0,
            local: false
        }
    ));
}

#[test]
fn local_validation_happens_before_writing() {
    let (mut device, fake) = device();
    let error = device.update_pins([(23, PinSetting::pull_up(20))]).unwrap_err();
    assert!(matches!(
        error,
        Error::PinConfigRejected {
            result: ResultCode::UNAVAILABLE_GPIO,
            gpio: 23,
            local: true
        }
    ));
    assert!(fake.state().feature_writes.is_empty());
}

#[test]
fn applied_config_must_match() {
    let (mut device, fake) = device();
    fake.state().drop_gpio5 = true;
    assert!(matches!(
        device.update_pins([(5, PinSetting::pull_up(10))]),
        Err(Error::PinConfigMismatch)
    ));
}

#[test]
fn pin_config_is_read_only_when_not_cached() {
    let (mut device, fake) = device();
    for _ in 0..2 {
        assert_eq!(device.pin_config().unwrap()[5], PinSetting::pull_up(20));
    }
    assert_eq!(fake.state().feature_reads, [REPORT_ID_PIN_CONFIG]);
}

#[test]
fn request_ids_stay_within_1_to_255_and_change_every_write() {
    let (mut device, fake) = device();
    for i in 0..50 {
        device.update_pins([(5, PinSetting::pull_up(i))]).unwrap();
    }
    let ids: Vec<u8> = fake.state().feature_writes.iter().map(|w| w[3]).collect();
    assert!(ids.iter().all(|&id| id != 0));
    assert!(ids.windows(2).all(|pair| pair[0] != pair[1]), "{ids:?}");
}

#[test]
fn update_pins_rejects_gpios_out_of_range() {
    let (mut device, fake) = device();
    let error = device.update_pins([(30, PinSetting::unused())]).unwrap_err();
    assert_eq!(error.to_string(), "GPIO30 is out of range (0-29)");
    assert!(fake.state().feature_writes.is_empty());
}

// --- status reports (7.4) ------------------------------------------------------------------------

#[test]
fn read_status_counts_missed_reports() {
    let (mut device, fake) = device();
    for seq in [10, 14, 15] {
        fake.queue(fake.current_status(seq, Reason::PERIODIC));
    }
    let timeout = Some(Duration::from_millis(1));
    assert_eq!(device.read_status(timeout).unwrap().unwrap().seq, 10);
    assert_eq!(device.missed_reports(), 0);
    assert_eq!(device.read_status(timeout).unwrap().unwrap().seq, 14);
    assert_eq!(device.missed_reports(), 3);
    device.read_status(timeout).unwrap();
    assert_eq!(device.missed_reports(), 3);
    assert!(device.read_status(timeout).unwrap().is_none());
}

#[test]
fn seq_wraps_and_host_requests_are_ignored() {
    let (mut device, fake) = device();
    fake.queue(fake.current_status(65535, Reason::PERIODIC));
    fake.queue(fake.current_status(7, Reason::HOST_REQUEST));
    fake.queue(fake.current_status(0, Reason::PERIODIC));
    for _ in 0..3 {
        device.read_status(None).unwrap();
    }
    assert_eq!(device.missed_reports(), 0);
}

#[test]
fn request_status_reads_the_current_state() {
    let (mut device, _) = device();
    let report = device.request_status().unwrap();
    assert!(report.reason.contains(Reason::HOST_REQUEST));
    assert_eq!(report.levels, PICO_AVAILABLE);
}

// --- outputs and polarity (7.5, 7.6) -----------------------------------------------------------------

#[test]
fn set_outputs_encodes_mask_and_value() {
    let (mut device, fake) = device();
    device.set_outputs([(7, true), (8, false)]).unwrap();
    let written = fake.state().output_writes.last().unwrap().clone();
    assert_eq!(written[0], REPORT_ID_OUTPUT);
    assert_eq!(
        protocol::decode_output(&written[1..]).unwrap(),
        (1 << 7 | 1 << 8, 1 << 7)
    );
}

#[test]
fn set_outputs_rejects_gpios_out_of_range() {
    let (mut device, fake) = device();
    let error = device.set_outputs([(7, true), (30, true)]).unwrap_err();
    assert_eq!(error.to_string(), "GPIO30 is out of range (0-29)");
    assert!(fake.state().output_writes.is_empty());
}

#[test]
fn on_off_follows_the_pull_direction() {
    let (mut device, _) = device();
    device
        .update_pins([(5, PinSetting::pull_up(20)), (6, PinSetting::pull_down(20))])
        .unwrap();
    let config = device.pin_config().unwrap();
    let report = StatusReport {
        monitored: 1 << 5 | 1 << 6,
        levels: 1 << 6,
        ..Default::default()
    };
    let on = device.on_off(&report, &config);
    assert_eq!((on[&5], on[&6]), (true, true)); // GPIO5 LOW with the pull-up, GPIO6 HIGH with the pull-down
    assert!(device.is_active_low(5, &config));
    device.set_polarity(5, false);
    assert!(!device.on_off(&report, &config)[&5]);
}

// --- reports of the wrong shape ------------------------------------------------------------------------

#[test]
fn short_feature_report_is_an_error() {
    let (mut device, fake) = device();
    fake.state().feature_override = Some(Box::new(|_| Ok(3)));
    assert_eq!(
        device.info().unwrap_err().to_string(),
        "feature report 2 is only 3 bytes long"
    );
}

#[test]
fn feature_report_with_another_id_is_an_error() {
    let (mut device, fake) = device();
    fake.state().feature_override = Some(Box::new(|buf| {
        buf[0] = 3;
        Ok(64)
    }));
    assert_eq!(
        device.info().unwrap_err().to_string(),
        "asked for feature report 2 but got report 3"
    );
}

#[test]
fn failed_feature_read_is_passed_on() {
    let (mut device, fake) = device();
    fake.state().feature_override = Some(Box::new(|_| Err(io::Error::new(io::ErrorKind::NotConnected, "gone"))));
    assert!(matches!(device.get_pin_config(), Err(Error::Io(source)) if source.kind() == io::ErrorKind::NotConnected));
}

#[test]
fn unexpected_input_report_is_an_error() {
    let (mut device, fake) = device();
    let mut report = vec![REPORT_ID_OUTPUT];
    report.extend([0u8; 63]);
    fake.state().reads.push_back(report);
    assert_eq!(
        device.read_status(None).unwrap_err().to_string(),
        "received an unexpected report ID 4"
    );
}

#[test]
fn short_status_report_is_an_error() {
    let (mut device, fake) = device();
    fake.state().reads.push_back(vec![REPORT_ID_STATUS, 0, 0]);
    assert_eq!(
        device.read_status(None).unwrap_err().to_string(),
        "status report is only 3 bytes long"
    );
}

#[test]
fn short_input_report_on_request_is_an_error() {
    let (mut device, fake) = device();
    fake.state().input_override = Some(1);
    assert_eq!(
        device.request_status().unwrap_err().to_string(),
        "could not read the status report"
    );
}

// --- choosing a device and errors (6, 8) --------------------------------------------------------------

fn entries(serials: &[&str]) -> Vec<DeviceEntry> {
    serials
        .iter()
        .enumerate()
        .map(|(i, serial)| DeviceEntry {
            path: format!("/dev/hidraw{i}"),
            serial: serial.to_string(),
            manufacturer: "yukke.org".into(),
            product: "hidpin".into(),
        })
        .collect()
}

#[test]
fn choose_device_takes_the_only_one_or_the_named_one() {
    assert_eq!(choose_device(&entries(&["A"]), None).unwrap().serial, "A");
    assert_eq!(choose_device(&entries(&["A", "B"]), Some("B")).unwrap().serial, "B");
}

#[test]
fn choose_device_without_a_match() {
    assert_eq!(
        choose_device(&[], None).unwrap_err().to_string(),
        "no hidpin device found"
    );
    assert_eq!(
        choose_device(&entries(&["A", "B"]), Some("C")).unwrap_err().to_string(),
        "no hidpin device found: no device with serial number C"
    );
}

#[test]
fn choose_device_refuses_to_guess() {
    let error = choose_device(&entries(&["A", "B"]), None).unwrap_err();
    assert!(matches!(&error, Error::SeveralDevices { serials } if serials == &["A", "B"]));
    assert_eq!(
        error.to_string(),
        "several devices are connected; pick one with --serial: A, B"
    );
}

#[test]
fn error_messages_follow_the_specification() {
    let cases = [
        (Error::ProtocolVersion { device_version: 2 }, "device speaks protocol version 2, this library speaks 1"),
        (
            Error::PinConfigRejected { result: ResultCode::UNAVAILABLE_GPIO, gpio: 23, local: true },
            "the pin configuration is invalid (nothing was sent to the device): the pin is not an available GPIO (GPIO23)",
        ),
        (
            Error::PinConfigRejected { result: ResultCode::BAD_LENGTH, gpio: RESULT_GPIO_NONE, local: false },
            "the device rejected the pin configuration: the payload is not 63 bytes long",
        ),
        (
            Error::PinConfigConflict { expected: 17, seen: 42 },
            "another process changed the pin configuration (sent request_id 17, read back 42)",
        ),
    ];
    for (error, want) in cases {
        assert_eq!(error.to_string(), want);
    }
}

#[test]
fn usb_ids_come_from_the_environment() {
    // One test changes the environment, so that tests running in parallel do not see it.
    std::env::remove_var("HIDPIN_VID");
    std::env::remove_var("HIDPIN_PID");
    assert_eq!(hidpin::usb_ids().unwrap(), (0x1209, 0x6870));
    std::env::set_var("HIDPIN_PID", "0x1234");
    std::env::set_var("HIDPIN_VID", "4660"); // decimal is accepted too
    assert_eq!(hidpin::usb_ids().unwrap(), (4660, 0x1234));
    for bad in ["zz", "0x10000", "-1"] {
        std::env::set_var("HIDPIN_PID", bad);
        let error = hidpin::usb_ids().unwrap_err().to_string();
        assert!(error.starts_with("HIDPIN_PID"), "{bad}: {error}");
    }
    std::env::remove_var("HIDPIN_VID");
    std::env::remove_var("HIDPIN_PID");
}
