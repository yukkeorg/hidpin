//! Finding devices and talking to one of them (docs/HOST_API.md 6-7).

use std::collections::{BTreeMap, HashMap};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use crate::error::{Error, Result};
use crate::protocol::{
    self, DeviceInfo, PinConfig, PinConfigReport, PinMode, PinSetting, Reason, ResultCode, StatusReport, GPIO_COUNT,
    REPORT_ID_DEVICE_INFO, REPORT_ID_OUTPUT, REPORT_ID_PIN_CONFIG, REPORT_ID_STATUS, REPORT_LEN,
};
use crate::transport::{Bus, Transport};

/// The vendor id pid.codes allocated to hidpin (<https://pid.codes/1209/6870/>).
pub const DEFAULT_VENDOR_ID: u16 = 0x1209;
/// The product id pid.codes allocated to hidpin.
pub const DEFAULT_PRODUCT_ID: u16 = 0x6870;

/// The top-level collection of the hidpin interface (PROTOCOL.md 1).
const USAGE_PAGE: u16 = 0xFF00;
const USAGE: u16 = 0x01;

/// The vendor and product id to look for: the defaults, or HIDPIN_VID / HIDPIN_PID when set (a
/// number in decimal or with a 0x prefix).
pub fn usb_ids() -> Result<(u16, u16)> {
    Ok((
        env_id("HIDPIN_VID", DEFAULT_VENDOR_ID)?,
        env_id("HIDPIN_PID", DEFAULT_PRODUCT_ID)?,
    ))
}

fn env_id(name: &str, fallback: u16) -> Result<u16> {
    let raw = std::env::var(name).unwrap_or_default();
    let raw = raw.trim();
    if raw.is_empty() {
        return Ok(fallback);
    }
    let parsed = match raw.strip_prefix("0x").or_else(|| raw.strip_prefix("0X")) {
        Some(hex) => u64::from_str_radix(hex, 16),
        None => raw.parse::<u64>(),
    };
    match parsed {
        Ok(value) if value <= 0xFFFF => Ok(value as u16),
        Ok(_) => Err(Error::InvalidArgument(format!(
            "{name} is out of range for 16 bits: '{raw}'"
        ))),
        Err(_) => Err(Error::InvalidArgument(format!(
            "{name} is not a 16-bit number: '{raw}'"
        ))),
    }
}

/// One hidpin device found on the host.
#[derive(Clone, PartialEq, Eq, Debug)]
pub struct DeviceEntry {
    pub path: String,
    pub serial: String,
    pub manufacturer: String,
    pub product: String,
}

/// Lists the connected hidpin devices, ordered by serial number, then by path. A HID interface
/// whose top-level collection is not Usage Page 0xFF00, Usage 0x01 is left out; when the report
/// descriptor cannot be read, opening the device checks the protocol version instead.
pub fn find_devices() -> Result<Vec<DeviceEntry>> {
    let (vendor_id, product_id) = usb_ids()?;
    find_devices_with(vendor_id, product_id)
}

#[cfg(target_os = "linux")]
fn find_devices_with(vendor_id: u16, product_id: u16) -> Result<Vec<DeviceEntry>> {
    let infos = crate::hidraw::enumerate_in(
        std::path::Path::new("/sys/class/hidraw"),
        std::path::Path::new("/dev"),
        vendor_id,
        product_id,
    )?;
    let mut entries: Vec<DeviceEntry> = infos
        .into_iter()
        .filter(|info| info.usage_page == 0 || (info.usage_page == USAGE_PAGE && info.usage == USAGE))
        .map(|info| DeviceEntry {
            path: info.path,
            serial: info.serial,
            manufacturer: info.manufacturer,
            product: info.product,
        })
        .collect();
    entries.sort_by(|a, b| (&a.serial, &a.path).cmp(&(&b.serial, &b.path)));
    Ok(entries)
}

#[cfg(not(target_os = "linux"))]
fn find_devices_with(_vendor_id: u16, _product_id: u16) -> Result<Vec<DeviceEntry>> {
    let _ = (USAGE_PAGE, USAGE);
    Err(Error::UnsupportedPlatform)
}

/// Picks the entry with the given serial number, or the only entry when `serial` is `None`.
pub fn choose_device(entries: &[DeviceEntry], serial: Option<&str>) -> Result<DeviceEntry> {
    let matching: Vec<&DeviceEntry> = entries
        .iter()
        .filter(|entry| serial.map_or(true, |serial| entry.serial == serial))
        .collect();
    match (matching.len(), serial) {
        (0, _) => Err(Error::DeviceNotFound {
            serial: serial.map(str::to_string),
        }),
        (1, _) | (_, Some(_)) => Ok(matching[0].clone()),
        _ => Err(Error::SeveralDevices {
            serials: matching.iter().map(|entry| entry.serial.clone()).collect(),
        }),
    }
}

fn open_transport(path: &str) -> Result<Box<dyn Transport>> {
    #[cfg(target_os = "linux")]
    {
        crate::hidraw::HidrawDevice::open(path)
            .map(|device| Box::new(device) as Box<dyn Transport>)
            .map_err(|source| Error::Open {
                path: path.to_string(),
                source: std::sync::Arc::new(source),
            })
    }
    #[cfg(not(target_os = "linux"))]
    {
        let _ = path;
        Err(Error::UnsupportedPlatform)
    }
}

/// The bus of the operating system: /dev/hidraw* on Linux.
#[derive(Clone, Copy, Debug, Default)]
pub struct SystemBus;

impl Bus for SystemBus {
    fn devices(&self) -> Result<Vec<DeviceEntry>> {
        find_devices()
    }

    fn open(&self, entry: &DeviceEntry) -> Result<Box<dyn Transport>> {
        open_transport(&entry.path)
    }
}

/// A small xorshift generator for request ids; they only have to differ between writers.
struct RequestIds(u64);

impl RequestIds {
    fn new() -> RequestIds {
        let nanos = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_nanos() as u64)
            .unwrap_or(1);
        RequestIds((nanos ^ u64::from(std::process::id()) << 32) | 1)
    }

    fn next(&mut self) -> u8 {
        let mut x = self.0;
        x ^= x << 13;
        x ^= x >> 7;
        x ^= x << 17;
        self.0 = x;
        (x % 255) as u8 + 1
    }
}

/// One open hidpin device. It is not meant to be shared between threads; move it to the thread
/// that uses it.
pub struct Device {
    transport: Box<dyn Transport>,
    serial: String,
    info: Option<DeviceInfo>,
    config: Option<PinConfig>,
    polarity: HashMap<u8, bool>,
    request_ids: RequestIds,
    last_request_id: u8,
    last_seq: Option<u16>,
    missed: u64,
}

impl Device {
    /// Wraps a transport, for example a fake one in tests. The protocol version is checked on the
    /// first call to `info`.
    pub fn new(transport: Box<dyn Transport>, serial: impl Into<String>) -> Device {
        Device {
            transport,
            serial: serial.into(),
            info: None,
            config: None,
            polarity: HashMap::new(),
            request_ids: RequestIds::new(),
            last_request_id: 0,
            last_seq: None,
            missed: 0,
        }
    }

    /// Opens the connected device with the given serial number, or the only one when `serial` is
    /// `None`, and checks its protocol version.
    pub fn open(serial: Option<&str>) -> Result<Device> {
        let entry = choose_device(&find_devices()?, serial)?;
        Device::open_path(&entry.path, &entry.serial)
    }

    /// Opens a device node such as /dev/hidraw0 and checks its protocol version.
    pub fn open_path(path: &str, serial: &str) -> Result<Device> {
        Device::open_on(open_transport(path)?, serial)
    }

    /// Wraps a transport and checks the protocol version before anything else is sent.
    pub(crate) fn open_on(transport: Box<dyn Transport>, serial: &str) -> Result<Device> {
        let mut device = Device::new(transport, serial);
        device.info()?;
        Ok(device)
    }

    /// Closes the device. Dropping it does the same.
    pub fn close(self) {}

    /// The serial number the device was opened by.
    pub fn serial(&self) -> &str {
        &self.serial
    }

    fn get_feature(&mut self, report_id: u8) -> Result<Vec<u8>> {
        let mut buf = [0u8; REPORT_LEN];
        buf[0] = report_id;
        let n = self.transport.get_feature_report(&mut buf)?;
        if n < REPORT_LEN {
            return Err(Error::Malformed(format!(
                "feature report {report_id} is only {n} bytes long"
            )));
        }
        if buf[0] != report_id {
            return Err(Error::Malformed(format!(
                "asked for feature report {report_id} but got report {}",
                buf[0]
            )));
        }
        Ok(buf[1..].to_vec())
    }

    /// The device information, read once and checked for the protocol version.
    pub fn info(&mut self) -> Result<DeviceInfo> {
        if let Some(info) = &self.info {
            return Ok(info.clone());
        }
        let info = protocol::decode_device_info(&self.get_feature(REPORT_ID_DEVICE_INFO)?)?;
        if info.protocol_version != protocol::PROTOCOL_VERSION {
            return Err(Error::ProtocolVersion {
                device_version: info.protocol_version,
            });
        }
        self.info = Some(info.clone());
        Ok(info)
    }

    /// Reads the pin configuration report now, together with the result of the last write.
    pub fn get_pin_config(&mut self) -> Result<PinConfigReport> {
        let report = protocol::decode_pin_config(&self.get_feature(REPORT_ID_PIN_CONFIG)?)?;
        self.config = Some(report.config);
        Ok(report)
    }

    /// The pin configuration, read only when it has not been read yet.
    pub fn pin_config(&mut self) -> Result<PinConfig> {
        match self.config {
            Some(config) => Ok(config),
            None => Ok(self.get_pin_config()?.config),
        }
    }

    fn next_request_id(&mut self) -> u8 {
        loop {
            let id = self.request_ids.next();
            if id != self.last_request_id {
                self.last_request_id = id;
                return id;
            }
        }
    }

    /// Writes a whole pin configuration and verifies it was applied (docs/HOST_API.md 7.3).
    ///
    /// The library does not warn about RP2350 erratum E9; check `info()?.pull_down_unreliable()`
    /// and `config.pull_down_gpios()` when that matters.
    pub fn set_pin_config(&mut self, config: &PinConfig) -> Result<PinConfigReport> {
        let available = self.info()?.available;
        let (result, gpio) = protocol::validate_pin_config(config, available);
        if result != ResultCode::OK {
            return Err(Error::PinConfigRejected {
                result,
                gpio,
                local: true,
            });
        }
        let request_id = self.next_request_id();
        let mut report = vec![REPORT_ID_PIN_CONFIG];
        report.extend(protocol::encode_pin_config_set(config, request_id)?);
        self.transport.send_feature_report(&report)?;

        let read = self.get_pin_config()?;
        if read.request_id != request_id {
            return Err(Error::PinConfigConflict {
                expected: request_id,
                seen: read.request_id,
            });
        }
        if read.result != ResultCode::OK {
            return Err(Error::PinConfigRejected {
                result: read.result,
                gpio: read.result_gpio,
                local: false,
            });
        }
        if read.config != *config {
            return Err(Error::PinConfigMismatch);
        }
        Ok(read)
    }

    /// Changes some pins and keeps every other pin as it is.
    pub fn update_pins(&mut self, settings: impl IntoIterator<Item = (u8, PinSetting)>) -> Result<PinConfigReport> {
        let config = self.get_pin_config()?.config.with_pins(settings)?;
        self.set_pin_config(&config)
    }

    /// Asks for the current state with Get_Report(Input) (PROTOCOL.md 4.6).
    pub fn request_status(&mut self) -> Result<StatusReport> {
        let mut buf = [0u8; REPORT_LEN];
        buf[0] = REPORT_ID_STATUS;
        let n = self.transport.get_input_report(&mut buf)?;
        if n < REPORT_LEN || buf[0] != REPORT_ID_STATUS {
            return Err(Error::Malformed("could not read the status report".into()));
        }
        protocol::decode_status(&buf[1..])
    }

    /// Reads one input report with the report ID first; 0 when the timeout expires.
    pub(crate) fn read_raw(&mut self, buf: &mut [u8], timeout: Option<Duration>) -> Result<usize> {
        Ok(self.transport.read(buf, timeout)?)
    }

    /// Waits for the next status report; `None` when the timeout expires (`None` timeout: wait
    /// forever).
    pub fn read_status(&mut self, timeout: Option<Duration>) -> Result<Option<StatusReport>> {
        let mut buf = [0u8; REPORT_LEN];
        let n = self.read_raw(&mut buf, timeout)?;
        if n == 0 {
            return Ok(None);
        }
        if buf[0] != REPORT_ID_STATUS {
            return Err(Error::Malformed(format!("received an unexpected report ID {}", buf[0])));
        }
        if n < REPORT_LEN {
            return Err(Error::Malformed(format!("status report is only {n} bytes long")));
        }
        let report = protocol::decode_status(&buf[1..])?;
        if !report.reason.contains(Reason::HOST_REQUEST) {
            if let Some(last) = self.last_seq {
                self.missed += u64::from(report.seq.wrapping_sub(last).wrapping_sub(1));
            }
            self.last_seq = Some(report.seq);
        }
        Ok(Some(report))
    }

    /// Status reports the host did not read, detected from gaps in seq.
    pub fn missed_reports(&self) -> u64 {
        self.missed
    }

    /// Drives the output pins in `mask` to the levels in `value` (PROTOCOL.md 7.1).
    pub fn write_outputs(&mut self, mask: u32, value: u32) -> Result<()> {
        let mut report = vec![REPORT_ID_OUTPUT];
        report.extend(protocol::encode_output(mask, value));
        self.transport.write(&report)?;
        Ok(())
    }

    /// Drives output pins (true = HIGH); GPIOs that are not output pins are ignored by the device.
    /// A GPIO outside 0-29 is an error and nothing is sent.
    pub fn set_outputs(&mut self, levels: impl IntoIterator<Item = (u8, bool)>) -> Result<()> {
        let (mut mask, mut value) = (0u32, 0u32);
        for (gpio, high) in levels {
            if usize::from(gpio) >= GPIO_COUNT {
                return Err(Error::InvalidArgument(format!(
                    "GPIO{gpio} is out of range (0-{})",
                    GPIO_COUNT - 1
                )));
            }
            mask |= 1 << gpio;
            if high {
                value |= 1 << gpio;
            }
        }
        self.write_outputs(mask, value)
    }

    /// Overrides which level counts as ON for one pin: `active_low` means LOW is ON.
    pub fn set_polarity(&mut self, gpio: u8, active_low: bool) {
        self.polarity.insert(gpio, active_low);
    }

    /// Whether LOW means ON: the override if there is one, otherwise whether the pin has the
    /// pull-up in `config`.
    pub fn is_active_low(&self, gpio: u8, config: &PinConfig) -> bool {
        match self.polarity.get(&gpio) {
            Some(&active_low) => active_low,
            None => config[gpio].mode == PinMode::PullUp,
        }
    }

    /// The ON/OFF of every monitored pin of a report.
    pub fn on_off(&self, report: &StatusReport, config: &PinConfig) -> BTreeMap<u8, bool> {
        protocol::mask_to_gpios(report.monitored)
            .into_iter()
            .map(|gpio| (gpio, report.level(gpio) != self.is_active_low(gpio, config)))
            .collect()
    }
}
