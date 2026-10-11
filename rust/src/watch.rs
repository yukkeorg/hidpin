//! The Watch layer: keeps one device connected for a long-running program (docs/HOST_API.md 9).
//!
//! `watch` starts a thread that owns the device and returns a `Watcher`, whose events are taken
//! with `recv`, `recv_timeout` or by iterating it, from any thread.

use std::collections::{BTreeMap, VecDeque};
use std::sync::{mpsc, Arc, Condvar, Mutex, MutexGuard};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant, SystemTime};

use crate::device::{Device, DeviceEntry, SystemBus};
use crate::error::{Error, Result};
use crate::protocol::{
    self, DeviceInfo, PinConfig, PinMode, Reason, ResultCode, StatusReport, ALL_GPIOS, GPIO_COUNT, REPORT_ID_STATUS,
    REPORT_LEN,
};
use crate::transport::Bus;

/// How often a watcher looks for its device while it is disconnected, by default.
pub const DEFAULT_SCAN_INTERVAL: Duration = Duration::from_secs(1);

/// How long one read of the device waits, which bounds how long a call waits for the watcher.
const READ_POLL: Duration = Duration::from_millis(50);
/// The shortest time between two writes of the target over a change made by another program.
const RESTORE_INTERVAL: Duration = Duration::from_secs(1);
/// How long to wait for CONFIG_CHANGED after writing the pin configuration before asking instead.
const SETTLE_TIMEOUT: Duration = Duration::from_secs(1);
/// How long one clock sample is trusted.
const CLOCK_WINDOW: Duration = Duration::from_secs(30);
/// Events waiting for the application; while they fill it, the watcher stops reading the device.
const OUT_CAPACITY: usize = 16;

// --- events ------------------------------------------------------------------------------------

/// Why output pins went back to their initial output level.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum OutputResetCause {
    /// The device reported a USB suspend or bus reset (OUTPUT_RESET).
    Suspend,
    /// The device was connected again after it had been disconnected.
    Reconnect,
    /// A pin configuration write made the pins outputs or changed their initial output level.
    PinConfig,
}

impl OutputResetCause {
    pub fn description(self) -> &'static str {
        match self {
            OutputResetCause::Suspend => "USB suspend or bus reset",
            OutputResetCause::Reconnect => "reconnected",
            OutputResetCause::PinConfig => "pin configuration written",
        }
    }
}

/// The ON/OFF of a monitored pin changed.
#[derive(Clone, PartialEq, Eq, Debug)]
pub struct OnOffChange {
    pub gpio: u8,
    pub on: bool,
    /// The pin level after the change (true = HIGH).
    pub level: bool,
    /// The moment the change began in host time (accurate to a few milliseconds), when known.
    pub time: Option<SystemTime>,
    /// The same moment on the device clock, in microseconds since the device started.
    pub device_us: Option<u64>,
    /// True when no edge event reported the change: the watcher found it by comparing pin levels
    /// after edge events were lost or the device was disconnected. Such a change has no time.
    pub inferred: bool,
}

impl OnOffChange {
    pub fn has_time(&self) -> bool {
        self.device_us.is_some()
    }
}

/// One notification from a `Watcher` (docs/HOST_API.md 9.3).
#[derive(Clone, Debug)]
pub enum Event {
    /// The device was opened and, with a target pin configuration, made to follow it.
    Connected {
        serial: String,
        path: String,
        info: DeviceInfo,
        /// The pin configuration of the device from now on.
        pin_config: PinConfig,
        /// False for the first connection only.
        reconnected: bool,
    },
    /// The connection was lost; the watcher keeps looking for the same device.
    Disconnected { error: Error },
    /// The device is there but cannot be used (permissions, protocol version, ...). Reported once
    /// per cause, when it happens twice in a row; the watcher keeps trying.
    ConnectFailed { error: Error },
    /// A status report as received, including the ones the watcher asks for.
    StatusReceived {
        status: StatusReport,
        /// Status reports lost just before this one.
        missed: u64,
        /// The ON/OFF of every monitored pin in it.
        on: BTreeMap<u8, bool>,
    },
    /// The ON/OFF of monitored pins the watcher did not know before. It is not a change.
    InitialOnOff { on: BTreeMap<u8, bool> },
    /// The ON/OFF of a monitored pin changed.
    OnOffChange(OnOffChange),
    /// These output pins are at their initial output level now. The watcher never drives them back
    /// to earlier levels; the application decides whether to.
    OutputsReset { gpios: Vec<u8>, cause: OutputResetCause },
    /// The pin configuration of the device changed. Only a watcher without a target reports it.
    PinConfigChanged { config: PinConfig },
    /// Another program changed the pin configuration and the watcher wrote the target back.
    /// `count` grows each time; a count that keeps growing means a fight.
    PinConfigRestored { found: PinConfig, count: u64 },
}

// --- options and the application side -------------------------------------------------------------

/// What `watch` watches and how.
#[derive(Clone)]
pub struct WatchOptions {
    /// The device to watch. `None` takes the first one found and from then on waits for that one
    /// only; the watcher stops with `Error::SeveralDevices` if it finds several at once.
    pub serial: Option<String>,
    /// The target pin configuration, written whenever the device has another one. Build it with
    /// `PinConfig::unused().with_pins(...)`; pins it does not set are unused. `None` never writes
    /// the pin configuration and follows the one on the device.
    pub target_pins: Option<PinConfig>,
    /// Polarity overrides: true means LOW is ON. Other monitored pins are active low when they have
    /// the pull-up.
    pub active_low: BTreeMap<u8, bool>,
    /// How often to look for the device while it is disconnected.
    pub scan_interval: Duration,
    /// What finds and opens the device; `None` means the operating system (`SystemBus`).
    pub bus: Option<Arc<dyn Bus>>,
}

impl Default for WatchOptions {
    fn default() -> Self {
        WatchOptions {
            serial: None,
            target_pins: None,
            active_low: BTreeMap::new(),
            scan_interval: DEFAULT_SCAN_INTERVAL,
            bus: None,
        }
    }
}

type Request = Box<dyn FnOnce(&mut WatchLoop) + Send>;

struct Shared {
    state: Mutex<SharedState>,
    changed: Condvar,
}

struct SharedState {
    out: VecDeque<Event>,
    requests: VecDeque<Request>,
    stopping: bool,
    finished: bool,
    error: Option<Error>,
}

fn lock(shared: &Shared) -> MutexGuard<'_, SharedState> {
    shared.state.lock().unwrap_or_else(|poisoned| poisoned.into_inner())
}

/// Keeps one device connected for a long-running program: it waits for the device, reopens it
/// after it was unplugged, keeps its pin configuration at the target and reports what happens as
/// events. Its methods can be called from any thread. While the application does not take events,
/// the watcher stops reading the device; reports lost that way show up in `StatusReceived::missed`.
/// Dropping it closes it.
pub struct Watcher {
    shared: Arc<Shared>,
    thread: Mutex<Option<JoinHandle<()>>>,
}

/// Starts watching a device and returns at once; the device need not be connected yet.
pub fn watch(options: WatchOptions) -> Result<Watcher> {
    if options.scan_interval.is_zero() {
        return Err(Error::InvalidArgument("the scan interval must be positive".into()));
    }
    if let Some(target) = &options.target_pins {
        let (result, gpio) = protocol::validate_pin_config(target, ALL_GPIOS);
        if result != ResultCode::OK {
            return Err(Error::PinConfigRejected {
                result,
                gpio,
                local: true,
            });
        }
    }
    if let Some(gpio) = options.active_low.keys().find(|&&gpio| usize::from(gpio) >= GPIO_COUNT) {
        return Err(Error::InvalidArgument(format!(
            "GPIO{gpio} is out of range (0-{})",
            GPIO_COUNT - 1
        )));
    }
    let bus: Arc<dyn Bus> = match options.bus {
        Some(bus) => bus,
        None => {
            crate::device::usb_ids()?; // fail at once on a bad HIDPIN_VID / HIDPIN_PID
            Arc::new(SystemBus)
        }
    };
    let shared = Arc::new(Shared {
        state: Mutex::new(SharedState {
            out: VecDeque::new(),
            requests: VecDeque::new(),
            stopping: false,
            finished: false,
            error: None,
        }),
        changed: Condvar::new(),
    });
    let mut watch_loop = WatchLoop {
        shared: shared.clone(),
        bus,
        serial: options.serial,
        target: options.target_pins,
        active_low: options.active_low,
        interval: options.scan_interval,
        pending: VecDeque::new(),
        info: None,
        config: PinConfig::unused(),
        known: BTreeMap::new(),
        ever_connected: false,
        conn: None,
        failure: String::new(),
        failures: 0,
        restore_count: 0,
        last_restore: None,
        scan_at: Some(Instant::now()),
        restore_at: None,
        settle_at: None,
    };
    let thread = thread::Builder::new()
        .name("hidpin-watch".into())
        .spawn(move || watch_loop.run())
        .map_err(Error::from)?;
    Ok(Watcher {
        shared,
        thread: Mutex::new(Some(thread)),
    })
}

impl Watcher {
    /// Takes the next event, waiting as long as it takes; `None` once the watcher has stopped and
    /// every event has been taken (`error` then tells why).
    pub fn recv(&self) -> Option<Event> {
        self.take(None).unwrap_or(None)
    }

    /// Takes the next event, waiting at most `timeout`: `Ok(None)` when it expires, and
    /// `Err(Error::WatcherClosed)` once the watcher has stopped and every event has been taken.
    pub fn recv_timeout(&self, timeout: Duration) -> Result<Option<Event>> {
        self.take(Some(Instant::now() + timeout))
    }

    fn take(&self, deadline: Option<Instant>) -> Result<Option<Event>> {
        let mut state = lock(&self.shared);
        loop {
            if let Some(event) = state.out.pop_front() {
                self.shared.changed.notify_all();
                return Ok(Some(event));
            }
            if state.finished {
                return Err(Error::WatcherClosed);
            }
            state = match deadline {
                None => self.shared.changed.wait(state).unwrap_or_else(|p| p.into_inner()),
                Some(deadline) => {
                    let now = Instant::now();
                    if now >= deadline {
                        return Ok(None);
                    }
                    self.shared
                        .changed
                        .wait_timeout(state, deadline - now)
                        .unwrap_or_else(|p| p.into_inner())
                        .0
                }
            };
        }
    }

    /// The events as an iterator that ends when the watcher stops.
    pub fn iter(&self) -> impl Iterator<Item = Event> + '_ {
        std::iter::from_fn(move || self.recv())
    }

    /// Why the watcher stopped: `PinConfigRejected` when the target does not suit the board,
    /// `SeveralDevices`, or `None` when it was closed (and while it runs).
    pub fn error(&self) -> Option<Error> {
        lock(&self.shared).error.clone()
    }

    /// Stops the watcher and closes the device. Events not yet taken are dropped.
    pub fn close(&self) {
        {
            let mut state = lock(&self.shared);
            state.stopping = true;
            self.shared.changed.notify_all();
        }
        let thread = self.thread.lock().unwrap_or_else(|p| p.into_inner()).take();
        if let Some(thread) = thread {
            let _ = thread.join();
        }
    }

    /// Drives output pins like `Device::set_outputs`. Fails with `Error::DeviceDisconnected` while
    /// the device is disconnected; the levels are not remembered for later.
    pub fn set_outputs(&self, levels: impl IntoIterator<Item = (u8, bool)>) -> Result<()> {
        let levels: Vec<(u8, bool)> = levels.into_iter().collect();
        self.call(move |l| match &mut l.conn {
            None => Err(Error::DeviceDisconnected),
            Some(conn) => conn.device.set_outputs(levels),
        })
    }

    /// Replaces the target pin configuration; a watcher without one gets one.
    ///
    /// It is checked against the board once the watcher has met it, and one that does not suit
    /// the board fails with `Error::PinConfigRejected`, keeping the old target. While the device is
    /// connected it is written at once; if that fails for another reason the new target stays and
    /// is written again later. While the device is disconnected it is written on the next
    /// connection.
    pub fn set_target_pins(&self, config: PinConfig) -> Result<()> {
        let (result, gpio) = protocol::validate_pin_config(&config, ALL_GPIOS);
        if result != ResultCode::OK {
            return Err(Error::PinConfigRejected {
                result,
                gpio,
                local: true,
            });
        }
        self.call(move |l| l.set_target(config))
    }

    /// Runs `f` on the thread that owns the device and returns its result.
    fn call<T: Send + 'static>(&self, f: impl FnOnce(&mut WatchLoop) -> Result<T> + Send + 'static) -> Result<T> {
        let (reply, answer) = mpsc::sync_channel(1);
        {
            let mut state = lock(&self.shared);
            if state.finished || state.stopping {
                return Err(Error::WatcherClosed);
            }
            state.requests.push_back(Box::new(move |l: &mut WatchLoop| {
                let _ = reply.send(f(l));
            }));
            self.shared.changed.notify_all();
        }
        // A request still queued when the watcher stops is dropped with its sender.
        answer.recv().unwrap_or(Err(Error::WatcherClosed))
    }
}

impl<'a> IntoIterator for &'a Watcher {
    type Item = Event;
    type IntoIter = Box<dyn Iterator<Item = Event> + 'a>;

    fn into_iter(self) -> Self::IntoIter {
        Box::new(self.iter())
    }
}

impl Drop for Watcher {
    fn drop(&mut self) {
        self.close();
    }
}

// --- the thread that owns the device ---------------------------------------------------------

/// An error that stops the watcher.
struct Fatal(Error);

type Step = std::result::Result<(), Fatal>;

/// Converts device times to host times. A report reaches the host some time after the device
/// stamped it, so the sample with the smallest host-minus-device difference is the most accurate.
/// The best sample of the current and the previous window is used, so the estimate follows the
/// drift between the two clocks.
#[derive(Default)]
pub(crate) struct DeviceClock {
    cur: Option<(Instant, u64)>,
    prev: Option<(Instant, u64)>,
    cur_start: Option<Instant>,
}

/// Whether sample `a` has a smaller host-minus-device difference than sample `b`.
fn closer(a: (Instant, u64), b: (Instant, u64)) -> bool {
    let host = if a.0 >= b.0 {
        (a.0 - b.0).as_nanos() as i128
    } else {
        -((b.0 - a.0).as_nanos() as i128)
    };
    let device = (i128::from(a.1) - i128::from(b.1)) * 1000;
    host < device
}

fn offset(base: Instant, micros: i128) -> Instant {
    let delta = Duration::from_micros(micros.unsigned_abs() as u64);
    if micros >= 0 {
        base + delta
    } else {
        base.checked_sub(delta).unwrap_or(base)
    }
}

impl DeviceClock {
    pub(crate) fn add(&mut self, host: Instant, device_us: u64) {
        let sample = (host, device_us);
        match (self.cur, self.cur_start) {
            (Some(cur), Some(start)) if host.duration_since(start) < CLOCK_WINDOW => {
                if closer(sample, cur) {
                    self.cur = Some(sample);
                }
            }
            _ => {
                self.prev = self.cur;
                self.cur = Some(sample);
                self.cur_start = Some(host);
            }
        }
    }

    /// The host time (`Instant`) of a device time.
    pub(crate) fn instant_at(&self, device_us: u64) -> Instant {
        let mut best = self.cur.expect("a sample was added");
        if let Some(prev) = self.prev {
            if closer(prev, best) {
                best = prev;
            }
        }
        offset(best.0, i128::from(device_us) - i128::from(best.1))
    }

    pub(crate) fn at(&self, device_us: u64) -> SystemTime {
        let at = self.instant_at(device_us);
        let (now, wall) = (Instant::now(), SystemTime::now());
        if at <= now {
            wall - (now - at)
        } else {
            wall + (at - now)
        }
    }
}

/// One open device, from opening it until it is lost or closed.
struct Connection {
    device: Device,
    clock: DeviceClock,
    last_seq: Option<u16>,
    /// The seq of the status requested after connecting: interrupt reports up to it were sent
    /// before that status, which already reflects them.
    snapshot_seq: Option<u16>,
    /// Set from writing the pin configuration until the device reports CONFIG_CHANGED; reports sent
    /// before that still show the old configuration (PROTOCOL.md 6.4).
    awaiting_config: bool,
}

struct WatchLoop {
    shared: Arc<Shared>,
    bus: Arc<dyn Bus>,
    serial: Option<String>,
    target: Option<PinConfig>,
    active_low: BTreeMap<u8, bool>,
    interval: Duration,

    pending: VecDeque<Event>,
    info: Option<DeviceInfo>,
    /// The pin configuration of the device, as last seen.
    config: PinConfig,
    /// The ON/OFF of monitored pins, kept across connections.
    known: BTreeMap<u8, bool>,
    ever_connected: bool,
    conn: Option<Connection>,
    failure: String,
    failures: u32,
    restore_count: u64,
    last_restore: Option<Instant>,
    scan_at: Option<Instant>,
    restore_at: Option<Instant>,
    settle_at: Option<Instant>,
}

fn due(at: Option<Instant>, now: Instant) -> bool {
    at.is_some_and(|at| now >= at)
}

impl WatchLoop {
    fn run(&mut self) {
        let result = self.main_loop();
        self.drop_connection();
        let mut state = lock(&self.shared);
        match result {
            Err(Fatal(error)) => {
                state.out.extend(self.pending.drain(..)); // deliver what happened before the error
                state.error = Some(error);
            }
            Ok(()) => {
                state.out.clear();
                self.pending.clear();
            }
        }
        state.finished = true;
        // Dropping the queued requests drops their reply senders, which makes the callers fail with
        // WatcherClosed.
        state.requests.clear();
        self.shared.changed.notify_all();
    }

    fn main_loop(&mut self) -> Step {
        loop {
            let requests: Vec<Request> = {
                let mut state = lock(&self.shared);
                while !self.pending.is_empty() && state.out.len() < OUT_CAPACITY {
                    state.out.push_back(self.pending.pop_front().expect("not empty"));
                    self.shared.changed.notify_all();
                }
                if state.stopping {
                    return Ok(());
                }
                state.requests.drain(..).collect()
            };
            if !requests.is_empty() {
                for request in requests {
                    request(self);
                }
                continue;
            }

            let now = Instant::now();
            if due(self.scan_at, now) {
                self.scan_at = None;
                self.try_connect()?;
                continue;
            }
            if due(self.restore_at, now) {
                self.restore_at = None;
                self.restore_target()?;
                continue;
            }
            if due(self.settle_at, now) {
                self.settle_at = None;
                if let Some(conn) = &mut self.conn {
                    conn.awaiting_config = false;
                    self.snapshot();
                }
                continue;
            }

            let next = [self.scan_at, self.restore_at, self.settle_at]
                .into_iter()
                .flatten()
                .min();
            let wait = next.map(|at| at.saturating_duration_since(now));
            if !self.pending.is_empty() || self.conn.is_none() {
                let state = lock(&self.shared);
                let busy = !state.requests.is_empty()
                    || state.stopping
                    || (!self.pending.is_empty() && state.out.len() < OUT_CAPACITY);
                if !busy {
                    match wait {
                        None => drop(self.shared.changed.wait(state)),
                        Some(wait) => drop(self.shared.changed.wait_timeout(state, wait)),
                    }
                }
                continue;
            }

            let timeout = wait
                .map_or(READ_POLL, |wait| wait.min(READ_POLL))
                .max(Duration::from_millis(1));
            let mut buf = [0u8; REPORT_LEN];
            let read = self
                .conn
                .as_mut()
                .expect("connected")
                .device
                .read_raw(&mut buf, Some(timeout));
            match read {
                Err(error) => self.lost(error),
                Ok(0) => {}
                Ok(n) => self.received(&buf[..n], Instant::now())?,
            }
        }
    }

    fn emit(&mut self, event: Event) {
        self.pending.push_back(event);
    }

    /// Notes a failed attempt to use the device. A cause is reported when it happens twice in a
    /// row, which skips the moment after plugging in when udev has not set the permissions yet.
    fn failed(&mut self, error: Error) {
        let text = error.to_string();
        if text != self.failure {
            self.failure = text;
            self.failures = 0;
        }
        self.failures += 1;
        if self.failures == 2 {
            self.emit(Event::ConnectFailed { error });
        }
    }

    fn reset_failures(&mut self) {
        self.failure.clear();
        self.failures = 0;
    }

    fn try_connect(&mut self) -> Step {
        let entries = match self.bus.devices() {
            Ok(entries) => entries,
            Err(Error::UnsupportedPlatform) => return Err(Fatal(Error::UnsupportedPlatform)),
            Err(error) => {
                self.failed(error);
                self.scan_at = Some(Instant::now() + self.interval);
                return Ok(());
            }
        };
        let matching: Vec<DeviceEntry> = entries
            .into_iter()
            .filter(|entry| self.serial.as_ref().map_or(true, |serial| &entry.serial == serial))
            .collect();
        if matching.is_empty() {
            self.reset_failures();
            self.scan_at = Some(Instant::now() + self.interval);
            return Ok(());
        }
        if self.serial.is_none() && matching.len() > 1 {
            return Err(Fatal(Error::SeveralDevices {
                serials: matching.iter().map(|e| e.serial.clone()).collect(),
            }));
        }
        let entry = matching.into_iter().next().expect("not empty");
        self.serial = Some(entry.serial.clone());
        match self.connect(&entry) {
            Ok(Ok(())) => Ok(()),
            Ok(Err(fatal)) => Err(fatal),
            Err(error) => {
                self.failed(error);
                self.scan_at = Some(Instant::now() + self.interval);
                Ok(())
            }
        }
    }

    /// Opens the device. The outer error is a failed attempt; the inner one stops the watcher.
    fn connect(&mut self, entry: &DeviceEntry) -> Result<Step> {
        let mut device = Device::new(self.bus.open(entry)?, entry.serial.clone());
        let info = device.info()?;
        self.info = Some(info.clone());
        let mut found = device.get_pin_config()?.config;
        let mut driven = Vec::new();
        let mut awaiting_config = false;
        if let Some(target) = self.target {
            if found != target {
                match device.set_pin_config(&target) {
                    Err(rejected @ Error::PinConfigRejected { .. }) => return Ok(Err(Fatal(rejected))),
                    Err(error) => return Err(error),
                    Ok(_) => {}
                }
                driven = driven_outputs(&found, &target);
                found = target;
                awaiting_config = true;
            }
        }

        self.conn = Some(Connection {
            device,
            clock: DeviceClock::default(),
            last_seq: None,
            snapshot_seq: None,
            awaiting_config,
        });
        self.reset_failures();
        let reconnected = self.ever_connected;
        self.ever_connected = true;
        self.set_config(found);
        self.emit(Event::Connected {
            serial: entry.serial.clone(),
            path: entry.path.clone(),
            info,
            pin_config: found,
            reconnected,
        });

        // Losing the connection always sends the outputs back to their initial level (PROTOCOL.md 7.2).
        let outputs = protocol::mask_to_gpios(found.outputs_mask());
        if reconnected && !outputs.is_empty() {
            self.emit(Event::OutputsReset {
                gpios: outputs,
                cause: OutputResetCause::Reconnect,
            });
        } else if !driven.is_empty() {
            self.emit(Event::OutputsReset {
                gpios: driven,
                cause: OutputResetCause::PinConfig,
            });
        }
        if awaiting_config {
            self.settle_at = Some(Instant::now() + SETTLE_TIMEOUT);
        } else {
            self.snapshot();
        }
        Ok(Ok(()))
    }

    /// Closes the connection, if there is one.
    fn drop_connection(&mut self) {
        if self.conn.take().is_some() {
            self.restore_at = None;
            self.settle_at = None;
        }
    }

    /// Handles a connection that failed, usually because the device was unplugged.
    fn lost(&mut self, error: Error) {
        if self.conn.is_none() {
            return;
        }
        self.drop_connection();
        self.emit(Event::Disconnected { error });
        self.scan_at = Some(Instant::now() + self.interval);
    }

    fn received(&mut self, data: &[u8], at: Instant) -> Step {
        if data.len() < REPORT_LEN || data[0] != REPORT_ID_STATUS {
            return Ok(());
        }
        let Ok(report) = protocol::decode_status(&data[1..REPORT_LEN]) else {
            return Ok(());
        };
        let conn = self.conn.as_mut().expect("connected");
        let missed = conn
            .last_seq
            .map_or(0, |last| u64::from(report.seq.wrapping_sub(last).wrapping_sub(1)));
        conn.last_seq = Some(report.seq);
        self.status(report, at, missed)
    }

    /// Asks the device for its current state and takes it as the reference for later reports.
    fn snapshot(&mut self) {
        let conn = self.conn.as_mut().expect("connected");
        let report = match conn.device.request_status() {
            Ok(report) => report,
            Err(error) => return self.lost(error),
        };
        // With edge events still queued, the levels are ahead of the interrupt reports that will
        // carry those events; learn from the interrupt reports instead, so no event counts twice.
        conn.snapshot_seq = (report.seq != 0 && !report.more_events()).then_some(report.seq);
        // A snapshot never stops the watcher.
        let _ = self.status(report, Instant::now(), 0);
    }

    fn status(&mut self, report: StatusReport, at: Instant, missed: u64) -> Step {
        let on = self.on_off(&report);
        let conn = self.conn.as_mut().expect("connected");
        conn.clock.add(at, report.timestamp_us);
        let host_request = report.reason.contains(Reason::HOST_REQUEST);
        let mut stale = false;
        if !host_request {
            if let Some(snapshot) = conn.snapshot_seq {
                // seq wraps around, so compare the distance.
                let distance = report.seq.wrapping_sub(snapshot);
                if distance == 0 || distance >= 0x8000 {
                    stale = true;
                } else {
                    conn.snapshot_seq = None;
                }
            }
        }
        let config_changed = report.reason.contains(Reason::CONFIG_CHANGED);
        if config_changed && !stale {
            conn.awaiting_config = false;
            self.settle_at = None;
        }
        self.emit(Event::StatusReceived {
            status: report.clone(),
            missed,
            on,
        });
        if report.reason.contains(Reason::OUTPUT_RESET) {
            let outputs = protocol::mask_to_gpios(report.outputs);
            if !outputs.is_empty() {
                self.emit(Event::OutputsReset {
                    gpios: outputs,
                    cause: OutputResetCause::Suspend,
                });
            }
        }
        if config_changed {
            self.config_changed()?;
            if self.conn.is_none() {
                return Ok(());
            }
        }
        let awaiting = self.conn.as_ref().is_some_and(|c| c.awaiting_config);
        if stale || awaiting || (host_request && report.more_events()) {
            return Ok(());
        }
        self.absorb(&report);
        Ok(())
    }

    /// Updates the known ON/OFF from a status report and reports what changed.
    fn absorb(&mut self, report: &StatusReport) {
        for event in &report.events {
            let Some(&was) = self.known.get(&event.gpio) else {
                continue;
            };
            let on = event.level != self.is_active_low(event.gpio);
            if on == was {
                // An edge event always leaves the level before it, so the opposite change was lost.
                self.emit(Event::OnOffChange(OnOffChange {
                    gpio: event.gpio,
                    on: !on,
                    level: !event.level,
                    time: None,
                    device_us: None,
                    inferred: true,
                }));
            }
            let start = event.start_us(report.timestamp_us);
            let time = start.map(|start| self.conn.as_ref().expect("connected").clock.at(start));
            self.emit(Event::OnOffChange(OnOffChange {
                gpio: event.gpio,
                on,
                level: event.level,
                time,
                device_us: start,
                inferred: false,
            }));
            self.known.insert(event.gpio, on);
        }
        let mut learned = BTreeMap::new();
        for gpio in protocol::mask_to_gpios(report.monitored) {
            let on = report.level(gpio) != self.is_active_low(gpio);
            match self.known.get(&gpio) {
                None => {
                    learned.insert(gpio, on);
                }
                Some(&was) if was != on => self.emit(Event::OnOffChange(OnOffChange {
                    gpio,
                    on,
                    level: report.level(gpio),
                    time: None,
                    device_us: None,
                    inferred: true,
                })),
                Some(_) => {}
            }
            self.known.insert(gpio, on);
        }
        self.known.retain(|&gpio, _| report.is_monitored(gpio));
        if !learned.is_empty() {
            self.emit(Event::InitialOnOff { on: learned });
        }
    }

    fn is_active_low(&self, gpio: u8) -> bool {
        match self.active_low.get(&gpio) {
            Some(&active_low) => active_low,
            None => self.config[gpio].mode == PinMode::PullUp,
        }
    }

    fn on_off(&self, report: &StatusReport) -> BTreeMap<u8, bool> {
        protocol::mask_to_gpios(report.monitored)
            .into_iter()
            .map(|gpio| (gpio, report.level(gpio) != self.is_active_low(gpio)))
            .collect()
    }

    /// Records the pin configuration of the device and forgets the ON/OFF of pins that are no
    /// longer monitored or whose pull changed: their pin level is read anew (PROTOCOL.md 6.4).
    fn set_config(&mut self, config: PinConfig) {
        let old = self.config;
        self.known
            .retain(|&gpio, _| config[gpio].is_monitored() && config[gpio].mode == old[gpio].mode);
        self.config = config;
    }

    /// Follows a new configuration, or writes the target back when another program changed it.
    fn config_changed(&mut self) -> Step {
        let read = self.conn.as_mut().expect("connected").device.get_pin_config();
        let config = match read {
            Ok(report) => report.config,
            Err(error) => {
                self.lost(error);
                return Ok(());
            }
        };
        let changed = config != self.config;
        self.set_config(config);
        let Some(target) = self.target else {
            if changed {
                self.emit(Event::PinConfigChanged { config });
            }
            return Ok(());
        };
        if config == target {
            return Ok(());
        }
        if let Some(last) = self.last_restore {
            let since = last.elapsed();
            if since < RESTORE_INTERVAL {
                self.restore_at = Some(Instant::now() + (RESTORE_INTERVAL - since));
                return Ok(());
            }
        }
        self.restore_target()
    }

    fn restore_target(&mut self) -> Step {
        let Some(target) = self.target else {
            return Ok(());
        };
        let Some(conn) = self.conn.as_mut() else {
            return Ok(());
        };
        self.restore_at = None;
        self.last_restore = Some(Instant::now());
        let found = match conn.device.get_pin_config() {
            Ok(report) => report.config,
            Err(error) => {
                self.lost(error);
                return Ok(());
            }
        };
        self.set_config(found);
        if found == target {
            return Ok(());
        }
        match self.conn.as_mut().expect("connected").device.set_pin_config(&target) {
            Ok(_) => {}
            Err(rejected @ Error::PinConfigRejected { .. }) => return Err(Fatal(rejected)),
            Err(Error::PinConfigConflict { .. }) => {
                self.restore_at = Some(Instant::now() + RESTORE_INTERVAL);
                return Ok(());
            }
            Err(error) => {
                self.lost(error);
                return Ok(());
            }
        }
        self.restore_count += 1;
        self.emit(Event::PinConfigRestored {
            found,
            count: self.restore_count,
        });
        self.wrote(found, target);
        Ok(())
    }

    fn set_target(&mut self, config: PinConfig) -> Result<()> {
        if let Some(info) = &self.info {
            let (result, gpio) = protocol::validate_pin_config(&config, info.available);
            if result != ResultCode::OK {
                return Err(Error::PinConfigRejected {
                    result,
                    gpio,
                    local: true,
                });
            }
        }
        let previous = self.target.replace(config);
        let Some(conn) = self.conn.as_mut() else {
            return Ok(());
        };
        self.restore_at = None;
        let found = conn.device.get_pin_config()?.config;
        self.set_config(found);
        if found == config {
            return Ok(());
        }
        match self.conn.as_mut().expect("connected").device.set_pin_config(&config) {
            Ok(_) => {}
            Err(error @ Error::PinConfigRejected { .. }) => {
                self.target = previous;
                return Err(error);
            }
            Err(error @ Error::PinConfigConflict { .. }) => {
                self.restore_at = Some(Instant::now() + RESTORE_INTERVAL);
                return Err(error);
            }
            Err(error) => return Err(error),
        }
        self.wrote(found, config);
        Ok(())
    }

    /// Records that `config` was written over `found`.
    fn wrote(&mut self, found: PinConfig, config: PinConfig) {
        self.set_config(config);
        if let Some(conn) = self.conn.as_mut() {
            conn.awaiting_config = true;
        }
        self.settle_at = Some(Instant::now() + SETTLE_TIMEOUT);
        let driven = driven_outputs(&found, &config);
        if !driven.is_empty() {
            self.emit(Event::OutputsReset {
                gpios: driven,
                cause: OutputResetCause::PinConfig,
            });
        }
    }
}

/// The pins that writing `new` over `prev` drives to their initial output level: pins that become
/// outputs and outputs whose initial output level changes (PROTOCOL.md 6.4).
pub(crate) fn driven_outputs(prev: &PinConfig, new: &PinConfig) -> Vec<u8> {
    (0..GPIO_COUNT as u8)
        .filter(|&gpio| new[gpio].is_output() && prev[gpio] != new[gpio])
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::protocol::PinSetting;

    #[test]
    fn device_clock_uses_the_least_delayed_sample() {
        let base = Instant::now();
        let mut clock = DeviceClock::default();
        clock.add(base + Duration::from_millis(5), 1_000_000); // read 5 ms late
        clock.add(base + Duration::from_millis(1001), 2_000_000); // read 1 ms late
        clock.add(base + Duration::from_millis(2030), 3_000_000); // read 30 ms late
        assert_eq!(clock.instant_at(2_500_000), base + Duration::from_millis(1501));
    }

    #[test]
    fn device_clock_forgets_old_samples() {
        let base = Instant::now();
        let mut clock = DeviceClock::default();
        clock.add(base, 0); // a perfect sample, but the clocks drift apart afterwards
        for i in 1..=3u32 {
            let elapsed = CLOCK_WINDOW * i;
            clock.add(base + elapsed + Duration::from_millis(10), elapsed.as_micros() as u64);
        }
        let device = (CLOCK_WINDOW * 3).as_micros() as u64;
        assert_eq!(
            clock.instant_at(device),
            base + CLOCK_WINDOW * 3 + Duration::from_millis(10)
        );
    }

    #[test]
    fn device_clock_gives_wall_time() {
        let mut clock = DeviceClock::default();
        clock.add(Instant::now(), 5_000_000);
        let estimate = clock.at(4_000_000); // a second before the sample
        let ago = SystemTime::now().duration_since(estimate).unwrap();
        assert!(
            ago >= Duration::from_millis(900) && ago < Duration::from_millis(1500),
            "{ago:?}"
        );
    }

    #[test]
    fn driven_outputs_are_new_or_changed_outputs() {
        let prev = PinConfig::unused()
            .with_pins([
                (1, PinSetting::output(false)),
                (2, PinSetting::output(false)),
                (3, PinSetting::pull_up(20)),
                (4, PinSetting::output(true)),
            ])
            .unwrap();
        let new = PinConfig::unused()
            .with_pins([
                (1, PinSetting::output(false)), // unchanged
                (2, PinSetting::output(true)),  // initial level changed
                (3, PinSetting::output(false)), // became an output
                (4, PinSetting::unused()),      // no longer an output
            ])
            .unwrap();
        assert_eq!(driven_outputs(&prev, &new), vec![2, 3]);
    }

    #[test]
    fn output_reset_causes_describe_themselves() {
        let causes = [
            OutputResetCause::Suspend,
            OutputResetCause::Reconnect,
            OutputResetCause::PinConfig,
        ];
        let texts: Vec<_> = causes.iter().map(|c| c.description()).collect();
        assert_eq!(
            texts,
            ["USB suspend or bus reset", "reconnected", "pin configuration written"]
        );
    }
}
