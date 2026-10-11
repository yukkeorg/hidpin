//! The wire format of hidpin protocol version 1 (docs/PROTOCOL.md): constants, types, and the
//! encoding and validation of every report (docs/HOST_API.md 3-5). Payloads exclude the report ID
//! byte. Nothing here does I/O.

use std::fmt;
use std::ops::{BitOr, Index};

use crate::error::{Error, Result};

pub const PROTOCOL_VERSION: u8 = 1;

pub const REPORT_ID_STATUS: u8 = 0x01;
pub const REPORT_ID_DEVICE_INFO: u8 = 0x02;
pub const REPORT_ID_PIN_CONFIG: u8 = 0x03;
pub const REPORT_ID_OUTPUT: u8 = 0x04;

/// The payload length of the status, device information and pin configuration reports.
pub const REPORT_PAYLOAD_LEN: usize = 63;
/// The payload length of an output command.
pub const OUTPUT_PAYLOAD_LEN: usize = 8;
/// A report with its report ID.
pub const REPORT_LEN: usize = 1 + REPORT_PAYLOAD_LEN;

pub const GPIO_COUNT: usize = 30;
pub const EVENTS_PER_REPORT: usize = 7;
pub const DEFAULT_DEBOUNCE_MS: u8 = 20;
pub const RESULT_GPIO_NONE: u8 = 0xFF;
pub const SATURATED_AGE_US: u32 = 0xFFFF_FFFF;
pub const SDK_BOARD_LEN: usize = 40;

const STATUS_EVENTS_OFFSET: usize = 28;
const EVENT_LEN: usize = 5;
const SDK_BOARD_OFFSET: usize = 17;
const PIN_CONFIG_OFFSET: usize = 3;

/// The mask of every GPIO number.
pub(crate) const ALL_GPIOS: u32 = (1 << GPIO_COUNT) - 1;

fn malformed(message: String) -> Error {
    Error::Malformed(message)
}

fn u16_at(p: &[u8], at: usize) -> u16 {
    u16::from_le_bytes([p[at], p[at + 1]])
}

fn u32_at(p: &[u8], at: usize) -> u32 {
    u32::from_le_bytes([p[at], p[at + 1], p[at + 2], p[at + 3]])
}

fn u64_at(p: &[u8], at: usize) -> u64 {
    let mut bytes = [0u8; 8];
    bytes.copy_from_slice(&p[at..at + 8]);
    u64::from_le_bytes(bytes)
}

// --- flags ----------------------------------------------------------------------------

macro_rules! flags {
    ($(#[$doc:meta])* $name:ident { $($(#[$flag_doc:meta])* $flag:ident = $bit:expr,)* }) => {
        $(#[$doc])*
        #[derive(Clone, Copy, PartialEq, Eq, Hash, Default)]
        pub struct $name(pub u8);

        impl $name {
            $($(#[$flag_doc])* pub const $flag: $name = $name($bit);)*

            /// Whether every bit of `other` is set.
            pub fn contains(self, other: $name) -> bool {
                other.0 != 0 && self.0 & other.0 == other.0
            }

            /// The names of the known bits that are set, lowest first.
            pub fn names(self) -> Vec<&'static str> {
                let mut names = Vec::new();
                $(if self.0 & $bit != 0 {
                    names.push(stringify!($flag));
                })*
                names
            }
        }

        impl BitOr for $name {
            type Output = $name;
            fn bitor(self, other: $name) -> $name {
                $name(self.0 | other.0)
            }
        }

        impl fmt::Display for $name {
            fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
                let names = self.names();
                if names.is_empty() {
                    write!(f, "0")
                } else {
                    write!(f, "{}", names.join("|"))
                }
            }
        }

        impl fmt::Debug for $name {
            fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
                write!(f, "{}({})", stringify!($name), self)
            }
        }
    };
}

flags! {
    /// Why the device sent a status report (PROTOCOL.md 4.1). Unknown bits are kept.
    Reason {
        LEVEL_CHANGED = 0x01,
        OUTPUT_APPLIED = 0x02,
        CONFIG_CHANGED = 0x04,
        PERIODIC = 0x08,
        OUTPUT_RESET = 0x10,
        HOST_REQUEST = 0x80,
    }
}

flags! {
    /// Auxiliary flags of a status report (PROTOCOL.md 4.2).
    StatusFlags {
        OVERFLOW = 0x01,
        MORE_EVENTS = 0x02,
    }
}

flags! {
    /// Known problems of the hardware (PROTOCOL.md 5.3).
    Quirks {
        /// RP2350 erratum E9: internal pull-downs cannot hold an input low.
        PULL_DOWN_UNRELIABLE = 0x01,
    }
}

// --- enumerations ---------------------------------------------------------------------------

/// How the device uses one GPIO (PROTOCOL.md 6.1).
#[derive(Clone, Copy, PartialEq, Eq, Hash, Debug, Default)]
#[repr(u8)]
pub enum PinMode {
    #[default]
    Unused = 0,
    /// Monitored, no pull.
    Input = 1,
    PullUp = 2,
    PullDown = 3,
    Output = 4,
}

impl PinMode {
    /// True for the monitored modes: `Input`, `PullUp` and `PullDown`.
    pub fn is_input(self) -> bool {
        matches!(self, PinMode::Input | PinMode::PullUp | PinMode::PullDown)
    }
}

impl TryFrom<u8> for PinMode {
    type Error = Error;

    fn try_from(value: u8) -> Result<Self> {
        match value {
            0 => Ok(PinMode::Unused),
            1 => Ok(PinMode::Input),
            2 => Ok(PinMode::PullUp),
            3 => Ok(PinMode::PullDown),
            4 => Ok(PinMode::Output),
            _ => Err(malformed(format!("mode {value} is undefined"))),
        }
    }
}

/// The outcome of the last pin configuration Set (PROTOCOL.md 6.3); unknown values are kept.
/// It is the `Result` of docs/HOST_API.md, renamed so as not to hide Rust's own.
#[derive(Clone, Copy, PartialEq, Eq, Hash, Debug)]
pub struct ResultCode(pub u8);

impl ResultCode {
    pub const OK: ResultCode = ResultCode(0);
    pub const BAD_LENGTH: ResultCode = ResultCode(1);
    pub const BAD_MODE: ResultCode = ResultCode(2);
    pub const UNAVAILABLE_GPIO: ResultCode = ResultCode(3);
    pub const UNUSED_WITH_PARAM: ResultCode = ResultCode(4);
    pub const BAD_OUTPUT_LEVEL: ResultCode = ResultCode(5);

    /// One line of English for the result (docs/HOST_API.md 4.1).
    pub fn message(self) -> String {
        match self {
            ResultCode::OK => "applied".into(),
            ResultCode::BAD_LENGTH => "the payload is not 63 bytes long".into(),
            ResultCode::BAD_MODE => "mode is outside 0-4".into(),
            ResultCode::UNAVAILABLE_GPIO => "the pin is not an available GPIO".into(),
            ResultCode::UNUSED_WITH_PARAM => "an unused pin was given a parameter".into(),
            ResultCode::BAD_OUTPUT_LEVEL => "the initial output level is neither 0 nor 1".into(),
            ResultCode(other) => format!("result {other}"),
        }
    }
}

impl fmt::Display for ResultCode {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.0)
    }
}

/// The board type in the device information (PROTOCOL.md 5.1); unknown values are kept.
#[derive(Clone, Copy, PartialEq, Eq, Hash, Debug, Default)]
pub struct Board(pub u8);

impl Board {
    /// A board without a preset; see `DeviceInfo::sdk_board`.
    pub const OTHER: Board = Board(0);
    pub const PICO: Board = Board(1);
    pub const QTPY_RP2040: Board = Board(2);
    pub const PICO2: Board = Board(3);
}

/// The product name of a board with a preset, otherwise its Pico SDK name when reported.
pub fn board_name(board: Board, sdk_board: &str) -> String {
    match board {
        Board::PICO => "Raspberry Pi Pico".into(),
        Board::QTPY_RP2040 => "Adafruit QT Py RP2040".into(),
        Board::PICO2 => "Raspberry Pi Pico 2".into(),
        _ if !sdk_board.is_empty() => sdk_board.into(),
        Board::OTHER => "other board".into(),
        Board(other) => format!("unknown board {other}"),
    }
}

/// The microcontroller in the device information (PROTOCOL.md 5.2); 0 means not reported.
#[derive(Clone, Copy, PartialEq, Eq, Hash, Debug, Default)]
pub struct Chip(pub u8);

impl Chip {
    pub const RP2040: Chip = Chip(1);
    pub const RP2350A: Chip = Chip(2);

    pub fn name(self) -> String {
        match self {
            Chip(0) => "not reported".into(),
            Chip::RP2040 => "RP2040".into(),
            Chip::RP2350A => "RP2350A".into(),
            Chip(other) => format!("unknown chip {other}"),
        }
    }
}

/// The GPIO numbers whose bit is set, ascending.
pub fn mask_to_gpios(mask: u32) -> Vec<u8> {
    (0..GPIO_COUNT as u8).filter(|gpio| mask >> gpio & 1 != 0).collect()
}

// --- status report -----------------------------------------------------------------------------

/// One confirmed change of a pin level (PROTOCOL.md 4.4).
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub struct EdgeEvent {
    pub gpio: u8,
    /// The pin level after the change (true = HIGH).
    pub level: bool,
    /// Microseconds from the change to the timestamp of the report.
    pub age_us: u32,
}

impl EdgeEvent {
    /// True when the change is older than the 32-bit age can express.
    pub fn saturated(&self) -> bool {
        self.age_us == SATURATED_AGE_US
    }

    /// The device time the change began, or `None` when it is saturated.
    pub fn start_us(&self, report_timestamp_us: u64) -> Option<u64> {
        if self.saturated() {
            None
        } else {
            Some(report_timestamp_us.wrapping_sub(u64::from(self.age_us)))
        }
    }
}

/// A status report, report ID 1 (PROTOCOL.md 4).
#[derive(Clone, PartialEq, Eq, Debug, Default)]
pub struct StatusReport {
    pub seq: u16,
    pub reason: Reason,
    pub flags: StatusFlags,
    pub monitored: u32,
    pub outputs: u32,
    pub levels: u32,
    pub timestamp_us: u64,
    pub events: Vec<EdgeEvent>,
}

impl StatusReport {
    pub fn overflow(&self) -> bool {
        self.flags.contains(StatusFlags::OVERFLOW)
    }

    pub fn more_events(&self) -> bool {
        self.flags.contains(StatusFlags::MORE_EVENTS)
    }

    /// The pin level or output level of a GPIO (true = HIGH).
    pub fn level(&self, gpio: u8) -> bool {
        self.levels >> gpio & 1 != 0
    }

    pub fn is_monitored(&self, gpio: u8) -> bool {
        self.monitored >> gpio & 1 != 0
    }

    pub fn is_output(&self, gpio: u8) -> bool {
        self.outputs >> gpio & 1 != 0
    }

    /// Builds the payload of the report; it fails with more than 7 events.
    pub fn encode(&self) -> Result<Vec<u8>> {
        if self.events.len() > EVENTS_PER_REPORT {
            return Err(Error::InvalidArgument(format!(
                "at most {EVENTS_PER_REPORT} events fit in one report"
            )));
        }
        let mut p = vec![0u8; REPORT_PAYLOAD_LEN];
        p[0..2].copy_from_slice(&self.seq.to_le_bytes());
        p[2] = self.reason.0;
        p[3] = self.flags.0;
        p[4] = self.events.len() as u8;
        p[8..12].copy_from_slice(&self.monitored.to_le_bytes());
        p[12..16].copy_from_slice(&self.outputs.to_le_bytes());
        p[16..20].copy_from_slice(&self.levels.to_le_bytes());
        p[20..28].copy_from_slice(&self.timestamp_us.to_le_bytes());
        for (i, event) in self.events.iter().enumerate() {
            let at = STATUS_EVENTS_OFFSET + EVENT_LEN * i;
            p[at] = (event.gpio & 0x1F) | if event.level { 0x80 } else { 0 };
            p[at + 1..at + 5].copy_from_slice(&event.age_us.to_le_bytes());
        }
        Ok(p)
    }
}

/// Parses the payload of a status report.
pub fn decode_status(p: &[u8]) -> Result<StatusReport> {
    if p.len() != REPORT_PAYLOAD_LEN {
        return Err(malformed(format!(
            "status payload must be {REPORT_PAYLOAD_LEN} bytes, got {}",
            p.len()
        )));
    }
    let count = usize::from(p[4]);
    if count > EVENTS_PER_REPORT {
        return Err(malformed(format!("event_count {count} exceeds {EVENTS_PER_REPORT}")));
    }
    let events = (0..count)
        .map(|i| {
            let at = STATUS_EVENTS_OFFSET + EVENT_LEN * i;
            EdgeEvent {
                gpio: p[at] & 0x1F,
                level: p[at] & 0x80 != 0,
                age_us: u32_at(p, at + 1),
            }
        })
        .collect();
    Ok(StatusReport {
        seq: u16_at(p, 0),
        reason: Reason(p[2]),
        flags: StatusFlags(p[3]),
        monitored: u32_at(p, 8),
        outputs: u32_at(p, 12),
        levels: u32_at(p, 16),
        timestamp_us: u64_at(p, 20),
        events,
    })
}

// --- device information ------------------------------------------------------------------------

/// The device information, report ID 2 (PROTOCOL.md 5).
#[derive(Clone, PartialEq, Eq, Debug, Default)]
pub struct DeviceInfo {
    pub protocol_version: u8,
    /// Major, minor, patch.
    pub firmware: [u8; 3],
    pub board: Board,
    pub gpio_count: u8,
    pub periodic_interval_ms: u16,
    pub available: u32,
    pub events_per_report: u8,
    pub event_queue_size: u8,
    pub chip: Chip,
    pub chip_revision: u8,
    pub quirks: Quirks,
    /// The Pico SDK board (PICO_BOARD) the firmware was built for; empty when not reported.
    pub sdk_board: String,
}

impl DeviceInfo {
    /// `"major.minor.patch"`.
    pub fn firmware_version(&self) -> String {
        format!("{}.{}.{}", self.firmware[0], self.firmware[1], self.firmware[2])
    }

    pub fn available_gpios(&self) -> Vec<u8> {
        mask_to_gpios(self.available)
    }

    pub fn board_name(&self) -> String {
        board_name(self.board, &self.sdk_board)
    }

    pub fn chip_name(&self) -> String {
        self.chip.name()
    }

    /// The internal pull-downs cannot hold an input low (RP2350 erratum E9).
    pub fn pull_down_unreliable(&self) -> bool {
        self.quirks.contains(Quirks::PULL_DOWN_UNRELIABLE)
    }

    /// Builds the payload of the report. An `sdk_board` longer than `SDK_BOARD_LEN` bytes is cut.
    pub fn encode(&self) -> Vec<u8> {
        let mut p = vec![0u8; REPORT_PAYLOAD_LEN];
        p[0] = self.protocol_version;
        p[1..4].copy_from_slice(&self.firmware);
        p[4] = self.board.0;
        p[5] = self.gpio_count;
        p[6..8].copy_from_slice(&self.periodic_interval_ms.to_le_bytes());
        p[8..12].copy_from_slice(&self.available.to_le_bytes());
        p[12] = self.events_per_report;
        p[13] = self.event_queue_size;
        p[14] = self.chip.0;
        p[15] = self.chip_revision;
        p[16] = self.quirks.0;
        let name = self.sdk_board.as_bytes();
        let len = name.len().min(SDK_BOARD_LEN);
        p[SDK_BOARD_OFFSET..SDK_BOARD_OFFSET + len].copy_from_slice(&name[..len]);
        p
    }
}

/// Parses the payload of the device information. `sdk_board` is ASCII up to the first 0 byte;
/// each byte outside ASCII becomes U+FFFD.
pub fn decode_device_info(p: &[u8]) -> Result<DeviceInfo> {
    if p.len() != REPORT_PAYLOAD_LEN {
        return Err(malformed(format!(
            "device info payload must be {REPORT_PAYLOAD_LEN} bytes, got {}",
            p.len()
        )));
    }
    let name = &p[SDK_BOARD_OFFSET..SDK_BOARD_OFFSET + SDK_BOARD_LEN];
    let end = name.iter().position(|&b| b == 0).unwrap_or(name.len());
    let sdk_board = name[..end]
        .iter()
        .map(|&b| if b.is_ascii() { char::from(b) } else { '\u{FFFD}' })
        .collect();
    Ok(DeviceInfo {
        protocol_version: p[0],
        firmware: [p[1], p[2], p[3]],
        board: Board(p[4]),
        gpio_count: p[5],
        periodic_interval_ms: u16_at(p, 6),
        available: u32_at(p, 8),
        events_per_report: p[12],
        event_queue_size: p[13],
        chip: Chip(p[14]),
        chip_revision: p[15],
        quirks: Quirks(p[16]),
        sdk_board,
    })
}

// --- pin configuration -------------------------------------------------------------------------

/// How one GPIO is used: a mode plus its parameter (PROTOCOL.md 6.1).
#[derive(Clone, Copy, PartialEq, Eq, Hash, Debug, Default)]
pub struct PinSetting {
    pub mode: PinMode,
    pub param: u8,
}

impl PinSetting {
    pub fn unused() -> PinSetting {
        PinSetting {
            mode: PinMode::Unused,
            param: 0,
        }
    }

    /// A monitored pin with its debounce time; a mode that is not an input mode is an error.
    pub fn monitor(mode: PinMode, debounce_ms: u8) -> Result<PinSetting> {
        if !mode.is_input() {
            return Err(Error::InvalidArgument(format!("{mode:?} is not an input mode")));
        }
        Ok(PinSetting {
            mode,
            param: debounce_ms,
        })
    }

    /// Monitored with the pull-up: a switch to GND.
    pub fn pull_up(debounce_ms: u8) -> PinSetting {
        PinSetting {
            mode: PinMode::PullUp,
            param: debounce_ms,
        }
    }

    /// Monitored with the pull-down.
    pub fn pull_down(debounce_ms: u8) -> PinSetting {
        PinSetting {
            mode: PinMode::PullDown,
            param: debounce_ms,
        }
    }

    /// Monitored without a pull.
    pub fn no_pull(debounce_ms: u8) -> PinSetting {
        PinSetting {
            mode: PinMode::Input,
            param: debounce_ms,
        }
    }

    /// An output pin that starts at, and returns to, the given initial output level.
    pub fn output(initial_high: bool) -> PinSetting {
        PinSetting {
            mode: PinMode::Output,
            param: u8::from(initial_high),
        }
    }

    pub fn is_monitored(&self) -> bool {
        self.mode.is_input()
    }

    pub fn is_output(&self) -> bool {
        self.mode == PinMode::Output
    }
}

/// The text form the command-line tools use: `off`, `nopull:N`, `pullup:N`, `pulldown:N`,
/// `out:low`, `out:high`.
impl fmt::Display for PinSetting {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self.mode {
            PinMode::Unused => write!(f, "off"),
            PinMode::Output => write!(f, "{}", if self.param != 0 { "out:high" } else { "out:low" }),
            PinMode::Input => write!(f, "nopull:{}", self.param),
            PinMode::PullUp => write!(f, "pullup:{}", self.param),
            PinMode::PullDown => write!(f, "pulldown:{}", self.param),
        }
    }
}

/// The pin configuration of every GPIO, indexed by GPIO number.
#[derive(Clone, Copy, PartialEq, Eq, Hash, Debug, Default)]
pub struct PinConfig(pub [PinSetting; GPIO_COUNT]);

impl PinConfig {
    /// Every GPIO unused: the start of a target pin configuration.
    pub fn unused() -> PinConfig {
        PinConfig::default()
    }

    /// The configuration the device starts with: the pull-up and `DEFAULT_DEBOUNCE_MS` for each
    /// available GPIO, unused for the rest (PROTOCOL.md 6.1).
    pub fn default_for(available: u32) -> PinConfig {
        let mut config = PinConfig::unused();
        for gpio in mask_to_gpios(available) {
            config.0[usize::from(gpio)] = PinSetting::pull_up(DEFAULT_DEBOUNCE_MS);
        }
        config
    }

    /// A copy with one pin replaced; a GPIO outside 0-29 is an error.
    pub fn with_pin(&self, gpio: u8, setting: PinSetting) -> Result<PinConfig> {
        if usize::from(gpio) >= GPIO_COUNT {
            return Err(Error::InvalidArgument(format!(
                "GPIO{gpio} is out of range (0-{})",
                GPIO_COUNT - 1
            )));
        }
        let mut config = *self;
        config.0[usize::from(gpio)] = setting;
        Ok(config)
    }

    /// A copy with some pins replaced; a GPIO outside 0-29 is an error.
    pub fn with_pins(&self, settings: impl IntoIterator<Item = (u8, PinSetting)>) -> Result<PinConfig> {
        let mut config = *self;
        for (gpio, setting) in settings {
            config = config.with_pin(gpio, setting)?;
        }
        Ok(config)
    }

    fn mask(&self, matches: impl Fn(&PinSetting) -> bool) -> u32 {
        self.0
            .iter()
            .enumerate()
            .filter(|(_, s)| matches(s))
            .fold(0, |mask, (gpio, _)| mask | 1 << gpio)
    }

    pub fn monitored_mask(&self) -> u32 {
        self.mask(PinSetting::is_monitored)
    }

    pub fn outputs_mask(&self) -> u32 {
        self.mask(PinSetting::is_output)
    }

    /// GPIOs monitored with the internal pull-down, ascending.
    pub fn pull_down_gpios(&self) -> Vec<u8> {
        mask_to_gpios(self.mask(|s| s.mode == PinMode::PullDown))
    }
}

impl Index<u8> for PinConfig {
    type Output = PinSetting;

    fn index(&self, gpio: u8) -> &PinSetting {
        &self.0[usize::from(gpio)]
    }
}

/// The pin configuration report, report ID 3 (PROTOCOL.md 6).
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub struct PinConfigReport {
    pub result: ResultCode,
    pub result_gpio: u8,
    pub request_id: u8,
    pub config: PinConfig,
}

impl PinConfigReport {
    pub fn encode(&self) -> Vec<u8> {
        let mut p = vec![0u8; REPORT_PAYLOAD_LEN];
        p[0] = self.result.0;
        p[1] = self.result_gpio;
        p[2] = self.request_id;
        for (gpio, setting) in self.config.0.iter().enumerate() {
            p[PIN_CONFIG_OFFSET + 2 * gpio] = setting.mode as u8;
            p[PIN_CONFIG_OFFSET + 2 * gpio + 1] = setting.param;
        }
        p
    }
}

/// Parses the payload of a pin configuration report; a mode above 4 is an error.
pub fn decode_pin_config(p: &[u8]) -> Result<PinConfigReport> {
    if p.len() != REPORT_PAYLOAD_LEN {
        return Err(malformed(format!(
            "pin config payload must be {REPORT_PAYLOAD_LEN} bytes, got {}",
            p.len()
        )));
    }
    let mut config = PinConfig::unused();
    for gpio in 0..GPIO_COUNT {
        let mode = PinMode::try_from(p[PIN_CONFIG_OFFSET + 2 * gpio]).map_err(|_| {
            malformed(format!(
                "mode {} of GPIO{gpio} is undefined",
                p[PIN_CONFIG_OFFSET + 2 * gpio]
            ))
        })?;
        config.0[gpio] = PinSetting {
            mode,
            param: p[PIN_CONFIG_OFFSET + 2 * gpio + 1],
        };
    }
    Ok(PinConfigReport {
        result: ResultCode(p[0]),
        result_gpio: p[1],
        request_id: p[2],
        config,
    })
}

/// Builds the payload the host writes; the result fields are ignored by the device.
pub fn encode_pin_config_set(config: &PinConfig, request_id: u8) -> Result<Vec<u8>> {
    if request_id == 0 {
        return Err(Error::InvalidArgument("request_id must be between 1 and 255".into()));
    }
    Ok(PinConfigReport {
        result: ResultCode::OK,
        result_gpio: RESULT_GPIO_NONE,
        request_id,
        config: *config,
    }
    .encode())
}

/// Repeats the validation the device does on a Set payload (PROTOCOL.md 6.3): the result, the GPIO
/// it refers to, and the request_id the device records.
pub fn validate_pin_config_payload(p: &[u8], available: u32) -> (ResultCode, u8, u8) {
    let request_id = p.get(2).copied().unwrap_or(0);
    if p.len() != REPORT_PAYLOAD_LEN {
        return (ResultCode::BAD_LENGTH, RESULT_GPIO_NONE, request_id);
    }
    for gpio in 0..GPIO_COUNT {
        let mode = p[PIN_CONFIG_OFFSET + 2 * gpio];
        let param = p[PIN_CONFIG_OFFSET + 2 * gpio + 1];
        let gpio_u8 = gpio as u8;
        if mode > PinMode::Output as u8 {
            return (ResultCode::BAD_MODE, gpio_u8, request_id);
        }
        if available >> gpio & 1 == 0 && mode != PinMode::Unused as u8 {
            return (ResultCode::UNAVAILABLE_GPIO, gpio_u8, request_id);
        }
        if mode == PinMode::Unused as u8 && param != 0 {
            return (ResultCode::UNUSED_WITH_PARAM, gpio_u8, request_id);
        }
        if mode == PinMode::Output as u8 && param > 1 {
            return (ResultCode::BAD_OUTPUT_LEVEL, gpio_u8, request_id);
        }
    }
    (ResultCode::OK, RESULT_GPIO_NONE, request_id)
}

/// Validates a pin configuration the way the device would, before anything is sent.
pub fn validate_pin_config(config: &PinConfig, available: u32) -> (ResultCode, u8) {
    let payload = PinConfigReport {
        result: ResultCode::OK,
        result_gpio: RESULT_GPIO_NONE,
        request_id: 1,
        config: *config,
    }
    .encode();
    let (result, gpio, _) = validate_pin_config_payload(&payload, available);
    (result, gpio)
}

// --- output command ------------------------------------------------------------------------------

/// The payload of an output command, report ID 4 (PROTOCOL.md 7.1).
pub fn encode_output(mask: u32, value: u32) -> Vec<u8> {
    let mut p = Vec::with_capacity(OUTPUT_PAYLOAD_LEN);
    p.extend_from_slice(&mask.to_le_bytes());
    p.extend_from_slice(&value.to_le_bytes());
    p
}

/// Parses the payload of an output command into `(mask, value)`.
pub fn decode_output(p: &[u8]) -> Result<(u32, u32)> {
    if p.len() != OUTPUT_PAYLOAD_LEN {
        return Err(malformed(format!(
            "output payload must be {OUTPUT_PAYLOAD_LEN} bytes, got {}",
            p.len()
        )));
    }
    Ok((u32_at(p, 0), u32_at(p, 4)))
}
