//! Simulated hidpin boards, to test code that uses hidpin without hardware.
//!
//! `FakeBus` stands in for the USB ports of the host and can be given to `watch` as its bus;
//! `FakeBoard` follows docs/PROTOCOL.md closely enough for the Watch layer. They match the Go
//! package hidpintest and the Python module hidpin.testing.
//!
//! ```
//! use hidpin::testing::{FakeBoard, FakeBus};
//! use hidpin::{Bus, Device};
//!
//! let board = FakeBoard::new("ABCD0123456789EF");
//! let bus = FakeBus::new(&[&board]);
//! let entry = bus.devices()?.remove(0);
//! let mut device = Device::new(bus.open(&entry)?, entry.serial);
//! assert_eq!(device.info()?.board_name(), "Raspberry Pi Pico");
//! # Ok::<(), hidpin::Error>(())
//! ```

use std::collections::VecDeque;
use std::io;
use std::sync::{Arc, Condvar, Mutex, MutexGuard};
use std::time::{Duration, Instant};

use crate::device::DeviceEntry;
use crate::error::{Error, Result};
use crate::protocol::{
    self, Board, DeviceInfo, EdgeEvent, PinConfig, PinConfigReport, Reason, ResultCode, StatusFlags, StatusReport,
    EVENTS_PER_REPORT, GPIO_COUNT, PROTOCOL_VERSION, REPORT_ID_DEVICE_INFO, REPORT_ID_OUTPUT, REPORT_ID_PIN_CONFIG,
    REPORT_ID_STATUS, RESULT_GPIO_NONE,
};
use crate::transport::{Bus, Transport};

/// GPIO0-22 and 26-28 of the Raspberry Pi Pico.
pub const PICO_AVAILABLE: u32 = 0x1C7F_FFFF;

/// Reports a handle keeps for the host; later ones are dropped, as hidraw does.
const QUEUE_LEN: usize = 64;

/// Whether an error is what the handles of an unplugged board return, like ENODEV from hidraw.
pub fn is_gone(error: &Error) -> bool {
    matches!(error, Error::Io(source) if source.kind() == io::ErrorKind::NotConnected)
}

fn gone() -> io::Error {
    io::Error::new(io::ErrorKind::NotConnected, "the device is gone")
}

fn lock<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
    mutex.lock().unwrap_or_else(|poisoned| poisoned.into_inner())
}

// --- the bus ----------------------------------------------------------------------------------

struct BusState {
    boards: Vec<FakeBoard>,
    nodes: usize,
}

/// A bus with boards that can be plugged in and out while a watcher runs. Clones share the bus.
#[derive(Clone)]
pub struct FakeBus(Arc<Mutex<BusState>>);

impl FakeBus {
    /// A bus with the given boards plugged in.
    pub fn new(boards: &[&FakeBoard]) -> FakeBus {
        let bus = FakeBus(Arc::new(Mutex::new(BusState {
            boards: Vec::new(),
            nodes: 0,
        })));
        for board in boards {
            bus.plug(board);
        }
        bus
    }

    /// Connects a board. A board that was unplugged starts afresh, as it lost power.
    pub fn plug(&self, board: &FakeBoard) {
        let mut state = lock(&self.0);
        board.plug(format!("/dev/hidraw{}", state.nodes));
        state.nodes += 1;
        state.boards.push(board.clone());
    }

    /// Disconnects a board: its open handles fail as if the device were gone, and it loses power.
    pub fn unplug(&self, board: &FakeBoard) {
        let mut state = lock(&self.0);
        state.boards.retain(|b| !Arc::ptr_eq(&b.0, &board.0));
        board.unplug();
    }
}

impl Bus for FakeBus {
    fn devices(&self) -> Result<Vec<DeviceEntry>> {
        let state = lock(&self.0);
        Ok(state
            .boards
            .iter()
            .map(|board| DeviceEntry {
                path: lock(&board.0.state).path.clone(),
                serial: board.0.serial.clone(),
                manufacturer: "yukke.org".into(),
                product: "hidpin".into(),
            })
            .collect())
    }

    fn open(&self, entry: &DeviceEntry) -> Result<Box<dyn Transport>> {
        let state = lock(&self.0);
        for board in &state.boards {
            if lock(&board.0.state).path == entry.path {
                return board.open();
            }
        }
        Err(Error::Open {
            path: entry.path.clone(),
            source: Arc::new(io::Error::new(io::ErrorKind::NotFound, "no such device")),
        })
    }
}

// --- the board ----------------------------------------------------------------------------------

struct BoardState {
    path: String,
    powered: bool,
    open_error: Option<io::ErrorKind>,
    info: DeviceInfo,
    config: PinConfig,
    result: ResultCode,
    result_gpio: u8,
    request_id: u8,
    inputs: u32, // the electrical value of every GPIO
    levels: u32, // pin levels of monitored pins and output levels of output pins
    next_seq: u16,
    last_seq: u16,
    clock_us: u64,
    overflow: bool,
    handles: Vec<Arc<HandleShared>>,
}

struct BoardShared {
    serial: String,
    state: Mutex<BoardState>,
}

/// A Raspberry Pi Pico running the hidpin firmware. Every GPIO input starts HIGH, like an open
/// switch with a pull-up. Status reports go to the handles that are open when they are sent.
/// Clones share the board.
#[derive(Clone)]
pub struct FakeBoard(Arc<BoardShared>);

impl FakeBoard {
    /// A board that is not plugged in yet.
    pub fn new(serial: &str) -> FakeBoard {
        FakeBoard(Arc::new(BoardShared {
            serial: serial.into(),
            state: Mutex::new(BoardState {
                path: String::new(),
                powered: false,
                open_error: None,
                info: DeviceInfo {
                    protocol_version: PROTOCOL_VERSION,
                    firmware: [0, 1, 0],
                    board: Board::PICO,
                    gpio_count: GPIO_COUNT as u8,
                    periodic_interval_ms: 1000,
                    available: PICO_AVAILABLE,
                    events_per_report: EVENTS_PER_REPORT as u8,
                    event_queue_size: 32,
                    ..DeviceInfo::default()
                },
                config: PinConfig::default_for(PICO_AVAILABLE),
                result: ResultCode::OK,
                result_gpio: RESULT_GPIO_NONE,
                request_id: 0,
                inputs: PICO_AVAILABLE,
                levels: 0,
                next_seq: 0,
                last_seq: 0,
                clock_us: 0,
                overflow: false,
                handles: Vec::new(),
            }),
        }))
    }

    pub fn serial(&self) -> &str {
        &self.0.serial
    }

    fn state(&self) -> MutexGuard<'_, BoardState> {
        lock(&self.0.state)
    }

    fn plug(&self, path: String) {
        let mut s = self.state();
        s.path = path;
        if s.powered {
            return;
        }
        s.powered = true;
        s.config = PinConfig::default_for(s.info.available);
        s.result = ResultCode::OK;
        s.result_gpio = RESULT_GPIO_NONE;
        s.request_id = 0;
        s.levels = s.inputs & s.config.monitored_mask();
        // The host sees the board some seconds after it started.
        s.next_seq = 0;
        s.last_seq = 0;
        s.clock_us = 5_000_000;
        s.overflow = false;
    }

    fn unplug(&self) {
        let mut s = self.state();
        fail_handles(&mut s);
        s.powered = false;
        s.path.clear();
    }

    fn open(&self) -> Result<Box<dyn Transport>> {
        let mut s = self.state();
        if let Some(kind) = s.open_error {
            return Err(Error::Open {
                path: s.path.clone(),
                source: Arc::new(io::Error::new(kind, format!("{kind}"))),
            });
        }
        let shared = Arc::new(HandleShared {
            queue: Mutex::new(HandleQueue {
                reports: VecDeque::new(),
                closed: false,
                failed: false,
            }),
            ready: Condvar::new(),
        });
        s.handles.push(shared.clone());
        Ok(Box::new(FakeHandle {
            board: self.clone(),
            shared,
        }))
    }

    /// Makes opening the board fail with an error of this kind, for example
    /// `io::ErrorKind::PermissionDenied`; `None` lets it open again.
    pub fn set_open_error(&self, kind: Option<io::ErrorKind>) {
        self.state().open_error = kind;
    }

    /// Changes the protocol version the board reports.
    pub fn set_protocol_version(&self, version: u8) {
        self.state().info.protocol_version = version;
    }

    /// Changes the electrical value of a GPIO. On a monitored pin this changes the pin level and
    /// sends an edge event, which began the debounce time before it was reported.
    pub fn set_input(&self, gpio: u8, high: bool) {
        let mut s = self.state();
        let bit = 1u32 << gpio;
        s.inputs = s.inputs & !bit | if high { bit } else { 0 };
        if !s.powered || !s.config[gpio].is_monitored() || s.levels & bit == s.inputs & bit {
            return;
        }
        s.levels = s.levels & !bit | s.inputs & bit;
        let age_us = u32::from(s.config[gpio].param) * 1000;
        send(
            &mut s,
            Reason::LEVEL_CHANGED,
            vec![EdgeEvent {
                gpio,
                level: high,
                age_us,
            }],
        );
    }

    /// Changes a monitored pin as if its edge event were dropped from a full event queue: the next
    /// report carries the new level and OVERFLOW.
    pub fn lose_input(&self, gpio: u8, high: bool) {
        let mut s = self.state();
        let bit = 1u32 << gpio;
        s.inputs = s.inputs & !bit | if high { bit } else { 0 };
        if s.config[gpio].is_monitored() {
            s.levels = s.levels & !bit | s.inputs & bit;
            s.overflow = true;
        }
    }

    /// Sends a periodic status report.
    pub fn periodic(&self) {
        send(&mut self.state(), Reason::PERIODIC, Vec::new());
    }

    /// Sends n status reports that the host never reads.
    pub fn skip_reports(&self, n: u16) {
        let mut s = self.state();
        s.next_seq = s.next_seq.wrapping_add(n);
        s.last_seq = s.next_seq.wrapping_sub(1);
    }

    /// A USB suspend: outputs go back to their initial output level and, if there are any, the next
    /// report says OUTPUT_RESET.
    pub fn suspend(&self) {
        let mut s = self.state();
        if reset_outputs(&mut s) {
            send(&mut s, Reason::OUTPUT_RESET, Vec::new());
        }
    }

    /// A USB reset that makes the host create the device node anew without the board losing
    /// power: open handles fail, and outputs go back to their initial output level.
    pub fn reenumerate(&self) {
        let mut s = self.state();
        fail_handles(&mut s);
        reset_outputs(&mut s);
    }

    /// Writes a pin configuration as another program would.
    pub fn write_pin_config(&self, config: &PinConfig) -> ResultCode {
        let mut s = self.state();
        let payload = protocol::encode_pin_config_set(config, 1).expect("request_id 1 is valid");
        write_config(&mut s, &payload);
        s.result
    }

    /// The pin configuration of the board.
    pub fn pin_config(&self) -> PinConfig {
        self.state().config
    }

    /// The pin level or output level of a GPIO.
    pub fn level(&self, gpio: u8) -> bool {
        self.state().levels >> gpio & 1 != 0
    }

    /// The number of open handles.
    pub fn open_count(&self) -> usize {
        self.state().handles.len()
    }
}

fn fail_handles(s: &mut BoardState) {
    for handle in s.handles.drain(..) {
        lock(&handle.queue).failed = true;
        handle.ready.notify_all();
    }
}

fn reset_outputs(s: &mut BoardState) -> bool {
    let outputs = s.config.outputs_mask();
    for gpio in protocol::mask_to_gpios(outputs) {
        let bit = 1u32 << gpio;
        s.levels = s.levels & !bit | if s.config[gpio].param != 0 { bit } else { 0 };
    }
    outputs != 0
}

fn status(s: &BoardState, reason: Reason, events: Vec<EdgeEvent>, seq: u16, flags: StatusFlags) -> StatusReport {
    StatusReport {
        seq,
        reason,
        flags,
        monitored: s.config.monitored_mask(),
        outputs: s.config.outputs_mask(),
        levels: s.levels,
        timestamp_us: s.clock_us,
        events,
    }
}

fn send(s: &mut BoardState, reason: Reason, events: Vec<EdgeEvent>) {
    s.clock_us += 1000;
    let flags = if s.overflow {
        StatusFlags::OVERFLOW
    } else {
        StatusFlags::default()
    };
    s.overflow = false;
    let report = status(s, reason, events, s.next_seq, flags);
    s.last_seq = s.next_seq;
    s.next_seq = s.next_seq.wrapping_add(1);
    let mut data = vec![REPORT_ID_STATUS];
    data.extend(report.encode().expect("at most one event"));
    for handle in &s.handles {
        let mut queue = lock(&handle.queue);
        if queue.reports.len() < QUEUE_LEN {
            queue.reports.push_back(data.clone());
            handle.ready.notify_all();
        }
    }
}

fn write_config(s: &mut BoardState, payload: &[u8]) {
    let (result, gpio, request_id) = protocol::validate_pin_config_payload(payload, s.info.available);
    s.result = result;
    s.result_gpio = gpio;
    s.request_id = request_id;
    if result != ResultCode::OK {
        return;
    }
    let next = protocol::decode_pin_config(payload).expect("validated").config;
    if next == s.config {
        return;
    }
    for gpio in 0..GPIO_COUNT as u8 {
        let (prev, setting, bit) = (s.config[gpio], next[gpio], 1u32 << gpio);
        if prev == setting || (prev.is_monitored() && setting.is_monitored() && prev.mode == setting.mode) {
            continue; // unchanged, or only the debounce time changed: the pin level stays
        }
        if setting.is_monitored() {
            s.levels = s.levels & !bit | s.inputs & bit;
        } else if setting.is_output() {
            s.levels = s.levels & !bit | if setting.param != 0 { bit } else { 0 };
        } else {
            s.levels &= !bit;
        }
    }
    s.config = next;
    send(s, Reason::CONFIG_CHANGED, Vec::new());
}

// --- a handle --------------------------------------------------------------------------------------

struct HandleQueue {
    reports: VecDeque<Vec<u8>>,
    closed: bool,
    failed: bool,
}

struct HandleShared {
    queue: Mutex<HandleQueue>,
    ready: Condvar,
}

/// One open handle of a board. Dropping it closes it.
struct FakeHandle {
    board: FakeBoard,
    shared: Arc<HandleShared>,
}

impl FakeHandle {
    fn check(&self) -> io::Result<()> {
        let queue = lock(&self.shared.queue);
        if queue.closed {
            return Err(io::Error::other("the handle is closed"));
        }
        if queue.failed {
            return Err(gone());
        }
        Ok(())
    }
}

impl Transport for FakeHandle {
    fn read(&mut self, buf: &mut [u8], timeout: Option<Duration>) -> io::Result<usize> {
        let deadline = timeout.map(|t| Instant::now() + t);
        let mut queue = lock(&self.shared.queue);
        loop {
            if queue.failed {
                return Err(gone());
            }
            if let Some(report) = queue.reports.pop_front() {
                let n = report.len().min(buf.len());
                buf[..n].copy_from_slice(&report[..n]);
                return Ok(n);
            }
            match deadline {
                None => queue = self.shared.ready.wait(queue).unwrap_or_else(|p| p.into_inner()),
                Some(deadline) => {
                    let now = Instant::now();
                    if now >= deadline {
                        return Ok(0);
                    }
                    queue = self
                        .shared
                        .ready
                        .wait_timeout(queue, deadline - now)
                        .unwrap_or_else(|p| p.into_inner())
                        .0;
                }
            }
        }
    }

    fn write(&mut self, report: &[u8]) -> io::Result<usize> {
        self.check()?;
        let mut s = self.board.state();
        if report.first() != Some(&REPORT_ID_OUTPUT) {
            return Err(io::Error::new(io::ErrorKind::InvalidInput, "unexpected output report"));
        }
        let Ok((mask, value)) = protocol::decode_output(&report[1..]) else {
            return Ok(report.len()); // the firmware ignores it
        };
        let mask = mask & s.config.outputs_mask();
        s.levels = s.levels & !mask | value & mask;
        send(&mut s, Reason::OUTPUT_APPLIED, Vec::new());
        Ok(report.len())
    }

    fn get_feature_report(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        self.check()?;
        let s = self.board.state();
        let payload = match buf[0] {
            REPORT_ID_DEVICE_INFO => s.info.encode(),
            REPORT_ID_PIN_CONFIG => PinConfigReport {
                result: s.result,
                result_gpio: s.result_gpio,
                request_id: s.request_id,
                config: s.config,
            }
            .encode(),
            _ => return Err(io::Error::new(io::ErrorKind::InvalidInput, "unexpected feature report")),
        };
        let n = payload.len().min(buf.len() - 1);
        buf[1..1 + n].copy_from_slice(&payload[..n]);
        Ok(n + 1)
    }

    fn send_feature_report(&mut self, report: &[u8]) -> io::Result<usize> {
        self.check()?;
        let mut s = self.board.state();
        if report.first() != Some(&REPORT_ID_PIN_CONFIG) {
            return Err(io::Error::new(io::ErrorKind::InvalidInput, "unexpected feature report"));
        }
        write_config(&mut s, &report[1..]);
        Ok(report.len())
    }

    fn get_input_report(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        self.check()?;
        let s = self.board.state();
        let report = status(&s, Reason::HOST_REQUEST, Vec::new(), s.last_seq, StatusFlags::default());
        let payload = report.encode().expect("no events");
        let n = payload.len().min(buf.len() - 1);
        buf[0] = REPORT_ID_STATUS;
        buf[1..1 + n].copy_from_slice(&payload[..n]);
        Ok(n + 1)
    }
}

impl Drop for FakeHandle {
    fn drop(&mut self) {
        lock(&self.shared.queue).closed = true;
        self.shared.ready.notify_all();
        self.board.state().handles.retain(|h| !Arc::ptr_eq(h, &self.shared));
    }
}
