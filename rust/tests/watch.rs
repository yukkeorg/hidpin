//! The Watch layer against simulated boards (the same cases as the Go and Python tests).

use std::collections::BTreeMap;
use std::io;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::thread;
use std::time::{Duration, Instant, SystemTime};

use hidpin::testing::{is_gone, FakeBoard, FakeBus, PICO_AVAILABLE};
use hidpin::{
    watch, Bus, DeviceEntry, Error, Event, OnOffChange, OutputResetCause, PinConfig, PinMode, PinSetting, Reason,
    Result, ResultCode, Transport, WatchOptions, Watcher,
};

const SERIAL_A: &str = "ABCD0123456789EF";
const SERIAL_B: &str = "FEDC9876543210AB";
const TIMEOUT: Duration = Duration::from_secs(3);

/// Starts a watcher on `bus`, looking for the device every 5 ms unless the options say otherwise.
fn start(bus: impl Bus + 'static, options: WatchOptions) -> Watcher {
    let scan_interval = if options.scan_interval == hidpin::DEFAULT_SCAN_INTERVAL {
        Duration::from_millis(5)
    } else {
        options.scan_interval
    };
    watch(WatchOptions {
        bus: Some(Arc::new(bus)),
        scan_interval,
        ..options
    })
    .unwrap()
}

fn pins(settings: &[(u8, PinSetting)]) -> PinConfig {
    PinConfig::unused().with_pins(settings.iter().copied()).unwrap()
}

fn next(watcher: &Watcher) -> Event {
    match watcher.recv_timeout(TIMEOUT) {
        Ok(Some(event)) => event,
        Ok(None) => panic!("no event arrived"),
        Err(error) => panic!("the watcher stopped: {:?} {error}", watcher.error()),
    }
}

/// Skips events until one that `pick` accepts arrives.
fn wait_for<T>(watcher: &Watcher, mut pick: impl FnMut(Event) -> Option<T>) -> T {
    loop {
        if let Some(found) = pick(next(watcher)) {
            return found;
        }
    }
}

macro_rules! await_event {
    ($watcher:expr, $pattern:pat => $value:expr) => {
        wait_for($watcher, |event| match event {
            $pattern => Some($value),
            _ => None,
        })
    };
}

/// Fails if an event that `bad` matches arrives within `wait`.
fn expect_no(watcher: &Watcher, wait: Duration, bad: impl Fn(&Event) -> bool) {
    let deadline = Instant::now() + wait;
    while let Some(remaining) = deadline.checked_duration_since(Instant::now()) {
        if let Ok(Some(event)) = watcher.recv_timeout(remaining) {
            assert!(!bad(&event), "unexpected {event:?}");
        }
    }
}

fn await_stop(watcher: &Watcher) -> Option<Error> {
    let deadline = Instant::now() + TIMEOUT;
    while Instant::now() < deadline {
        if let Err(Error::WatcherClosed) = watcher.recv_timeout(Duration::from_millis(100)) {
            return watcher.error();
        }
    }
    panic!("the watcher did not stop");
}

fn owner_target() -> PinConfig {
    pins(&[
        (5, PinSetting::pull_up(20)),
        (6, PinSetting::pull_down(0)),
        (7, PinSetting::output(false)),
    ])
}

/// A watcher with a target on a plugged-in board that knows the ON/OFF of its monitored pins: GPIO5
/// OFF (pull-up, HIGH) and GPIO6 ON (pull-down, HIGH).
fn owner() -> (Watcher, FakeBus, FakeBoard, PinConfig) {
    let board = FakeBoard::new(SERIAL_A);
    let bus = FakeBus::new(&[&board]);
    let target = owner_target();
    let watcher = start(
        bus.clone(),
        WatchOptions {
            target_pins: Some(target),
            ..Default::default()
        },
    );
    let initial = await_event!(&watcher, Event::InitialOnOff { on } => on);
    assert_eq!(initial, BTreeMap::from([(5, false), (6, true)]));
    (watcher, bus, board, target)
}

fn as_change(event: Event) -> Option<OnOffChange> {
    match event {
        Event::OnOffChange(change) => Some(change),
        _ => None,
    }
}

#[test]
fn writes_the_target_and_learns_on_off() {
    let board = FakeBoard::new(SERIAL_A);
    let target = owner_target();
    let watcher = start(
        FakeBus::new(&[&board]),
        WatchOptions {
            target_pins: Some(target),
            ..Default::default()
        },
    );

    let (serial, pin_config, reconnected) = await_event!(&watcher,
        Event::Connected { serial, pin_config, reconnected, .. } => (serial, pin_config, reconnected));
    assert_eq!((serial.as_str(), pin_config, reconnected), (SERIAL_A, target, false));
    assert_eq!(board.pin_config(), target);
    assert!(
        matches!(next(&watcher), Event::OutputsReset { gpios, cause: OutputResetCause::PinConfig } if gpios == [7])
    );
    let status = await_event!(&watcher, Event::StatusReceived { status, .. } => status);
    assert!(status.reason.contains(Reason::CONFIG_CHANGED));
    assert_eq!(status.monitored, 1 << 5 | 1 << 6);
    assert!(matches!(next(&watcher), Event::InitialOnOff { on } if on == BTreeMap::from([(5, false), (6, true)])));
}

#[test]
fn reports_timed_changes() {
    let (watcher, _, board, _) = owner();
    board.set_input(5, false);
    let (status, on) = await_event!(&watcher, Event::StatusReceived { status, on, .. } => (status, on));
    let change = as_change(next(&watcher)).expect("an ON/OFF change");
    assert_eq!(
        (change.gpio, change.on, change.level, change.inferred, change.has_time()),
        (5, true, false, false, true)
    );
    assert_eq!(change.device_us, Some(status.timestamp_us - 20_000));
    let age = SystemTime::now().duration_since(change.time.unwrap()).unwrap();
    assert!(age < Duration::from_secs(5));
    assert!(on[&5] && on[&6]);
}

#[test]
fn reconnects_after_unplugging() {
    let (watcher, bus, board, target) = owner();
    board.set_input(5, false);
    assert!(wait_for(&watcher, as_change).on);

    bus.unplug(&board);
    let error = await_event!(&watcher, Event::Disconnected { error } => error);
    assert!(is_gone(&error), "{error}");
    assert!(matches!(
        watcher.set_outputs([(7, true)]),
        Err(Error::DeviceDisconnected)
    ));
    board.set_input(5, true); // GPIO5 back to OFF
    board.set_input(6, false); // GPIO6 to OFF
    bus.plug(&board); // the board starts again with the default pin configuration

    assert!(await_event!(&watcher, Event::Connected { reconnected, .. } => reconnected));
    assert_eq!(board.pin_config(), target);
    assert!(
        matches!(next(&watcher), Event::OutputsReset { gpios, cause: OutputResetCause::Reconnect } if gpios == [7])
    );
    let mut changes = BTreeMap::new();
    while changes.len() < 2 {
        match next(&watcher) {
            Event::InitialOnOff { on } => panic!("pins known before were learned again: {on:?}"),
            Event::OnOffChange(change) => {
                changes.insert(change.gpio, change);
            }
            _ => {}
        }
    }
    for gpio in [5, 6] {
        let change = &changes[&gpio];
        assert!(
            !change.on && change.inferred && !change.has_time(),
            "GPIO{gpio}: {change:?}"
        );
    }
}

#[test]
fn stays_with_the_first_board() {
    let (a, b) = (FakeBoard::new(SERIAL_A), FakeBoard::new(SERIAL_B));
    let bus = FakeBus::new(&[&a]);
    let watcher = start(bus.clone(), WatchOptions::default());
    assert_eq!(
        await_event!(&watcher, Event::Connected { serial, .. } => serial),
        SERIAL_A
    );
    bus.unplug(&a);
    await_event!(&watcher, Event::Disconnected { .. } => ());
    bus.plug(&b);
    expect_no(&watcher, Duration::from_millis(100), |e| {
        matches!(e, Event::Connected { .. })
    });
    assert_eq!(b.open_count(), 0);
    bus.plug(&a);
    let (serial, reconnected) =
        await_event!(&watcher, Event::Connected { serial, reconnected, .. } => (serial, reconnected));
    assert_eq!((serial.as_str(), reconnected), (SERIAL_A, true));
}

#[test]
fn stops_when_it_cannot_choose_a_board() {
    let (a, b) = (FakeBoard::new(SERIAL_A), FakeBoard::new(SERIAL_B));
    let watcher = start(FakeBus::new(&[&a, &b]), WatchOptions::default());
    assert!(matches!(await_stop(&watcher), Some(Error::SeveralDevices { .. })));
    assert!(matches!(watcher.set_outputs([(7, true)]), Err(Error::WatcherClosed)));
}

#[test]
fn stops_when_the_target_does_not_suit_the_board() {
    let target = pins(&[(23, PinSetting::pull_up(20))]);
    let watcher = start(
        FakeBus::new(&[&FakeBoard::new(SERIAL_A)]),
        WatchOptions {
            target_pins: Some(target),
            ..Default::default()
        },
    );
    assert!(matches!(
        await_stop(&watcher),
        Some(Error::PinConfigRejected { gpio: 23, .. })
    ));
}

#[test]
fn rejects_bad_options() {
    let bus: Arc<dyn Bus> = Arc::new(FakeBus::new(&[]));
    let bad = PinConfig::unused()
        .with_pin(
            3,
            PinSetting {
                mode: PinMode::Unused,
                param: 1,
            },
        )
        .unwrap();
    for options in [
        WatchOptions {
            target_pins: Some(bad),
            ..Default::default()
        },
        WatchOptions {
            active_low: BTreeMap::from([(30, true)]),
            ..Default::default()
        },
        WatchOptions {
            scan_interval: Duration::ZERO,
            ..Default::default()
        },
    ] {
        assert!(watch(WatchOptions {
            bus: Some(bus.clone()),
            ..options
        })
        .is_err());
    }
}

#[test]
fn writes_the_target_back() {
    let (watcher, _, board, target) = owner();
    let other = target
        .with_pins([(5, PinSetting::unused()), (7, PinSetting::unused())])
        .unwrap();
    assert_eq!(board.write_pin_config(&other), ResultCode::OK);
    let (found, count) = await_event!(&watcher, Event::PinConfigRestored { found, count } => (found, count));
    assert_eq!((found, count), (other, 1));
    assert_eq!(board.pin_config(), target);
    assert!(
        matches!(next(&watcher), Event::OutputsReset { gpios, cause: OutputResetCause::PinConfig } if gpios == [7])
    );
    // GPIO5 was not monitored for a moment, so its ON/OFF is learned again.
    assert_eq!(
        await_event!(&watcher, Event::InitialOnOff { on } => on),
        BTreeMap::from([(5, false)])
    );
}

#[test]
fn without_target_follows_the_device() {
    let board = FakeBoard::new(SERIAL_A);
    let watcher = start(FakeBus::new(&[&board]), WatchOptions::default());
    let defaults = PinConfig::default_for(PICO_AVAILABLE);
    assert_eq!(
        await_event!(&watcher, Event::Connected { pin_config, .. } => pin_config),
        defaults
    );
    let initial = await_event!(&watcher, Event::InitialOnOff { on } => on);
    assert_eq!(initial.len(), 26);
    assert!(!initial[&5]);
    let other = defaults.with_pin(5, PinSetting::unused()).unwrap();
    board.write_pin_config(&other);
    assert_eq!(
        await_event!(&watcher, Event::PinConfigChanged { config } => config),
        other
    );
    expect_no(&watcher, Duration::from_millis(50), |e| {
        matches!(e, Event::PinConfigRestored { .. })
    });
    assert_eq!(board.pin_config(), other);
}

#[test]
fn infers_lost_changes() {
    let (watcher, _, board, _) = owner();

    board.skip_reports(2);
    board.periodic();
    assert_eq!(
        await_event!(&watcher, Event::StatusReceived { missed, .. } => missed),
        2
    );

    // The device dropped the edge event: only the level tells.
    board.lose_input(5, false);
    board.periodic();
    let change = wait_for(&watcher, as_change);
    assert_eq!(
        (change.gpio, change.on, change.inferred, change.has_time()),
        (5, true, true, false)
    );

    // GPIO5 went OFF without an edge event and then ON with one: the OFF is inferred from the edge
    // event, which must have left the other level.
    board.lose_input(5, true);
    board.set_input(5, false);
    let first = wait_for(&watcher, as_change);
    let second = as_change(next(&watcher)).expect("an ON/OFF change");
    assert!(!first.on && first.inferred);
    assert!(second.on && !second.inferred && second.has_time());
}

#[test]
fn polarity_override() {
    let watcher = start(
        FakeBus::new(&[&FakeBoard::new(SERIAL_A)]),
        WatchOptions {
            target_pins: Some(pins(&[(5, PinSetting::pull_up(20))])),
            active_low: BTreeMap::from([(5, false)]),
            ..Default::default()
        },
    );
    assert!(
        await_event!(&watcher, Event::InitialOnOff { on } => on)[&5],
        "GPIO5 HIGH should be ON"
    );
}

#[test]
fn reports_output_resets() {
    let (watcher, _, board, target) = owner();
    watcher.set_outputs([(7, true)]).unwrap();
    assert!(board.level(7));

    board.suspend();
    let (gpios, cause) = await_event!(&watcher, Event::OutputsReset { gpios, cause } => (gpios, cause));
    assert_eq!((gpios, cause), (vec![7], OutputResetCause::Suspend));
    assert!(!board.level(7), "the output was driven back");

    board.reenumerate();
    await_event!(&watcher, Event::Disconnected { .. } => ());
    let (reconnected, pin_config) =
        await_event!(&watcher, Event::Connected { reconnected, pin_config, .. } => (reconnected, pin_config));
    assert!(reconnected && pin_config == target);
    assert!(matches!(
        next(&watcher),
        Event::OutputsReset {
            cause: OutputResetCause::Reconnect,
            ..
        }
    ));
}

#[test]
fn set_target_pins() {
    let (watcher, bus, board, target) = owner();

    let changed = target.with_pin(8, PinSetting::output(true)).unwrap();
    watcher.set_target_pins(changed).unwrap();
    assert_eq!(board.pin_config(), changed);
    assert_eq!(await_event!(&watcher, Event::OutputsReset { gpios, .. } => gpios), [8]);
    assert!(board.level(8));

    let unsuitable = changed.with_pin(23, PinSetting::pull_up(20)).unwrap();
    assert!(matches!(
        watcher.set_target_pins(unsuitable),
        Err(Error::PinConfigRejected { .. })
    ));
    assert_eq!(board.pin_config(), changed);

    bus.unplug(&board);
    await_event!(&watcher, Event::Disconnected { .. } => ());
    let later = target.with_pin(5, PinSetting::pull_down(5)).unwrap();
    watcher.set_target_pins(later).unwrap();
    assert!(matches!(
        watcher.set_target_pins(unsuitable),
        Err(Error::PinConfigRejected { .. })
    ));
    bus.plug(&board);
    assert_eq!(
        await_event!(&watcher, Event::Connected { pin_config, .. } => pin_config),
        later
    );
    assert_eq!(board.pin_config(), later);
}

#[test]
fn set_target_pins_validates_before_reaching_the_device() {
    let (watcher, _, board, target) = owner();
    let bad = target
        .with_pin(
            3,
            PinSetting {
                mode: PinMode::Unused,
                param: 1,
            },
        )
        .unwrap();
    assert!(matches!(
        watcher.set_target_pins(bad),
        Err(Error::PinConfigRejected {
            gpio: 3,
            local: true,
            ..
        })
    ));
    assert_eq!(board.pin_config(), target);
}

#[test]
fn set_target_pins_to_the_current_one_writes_nothing() {
    let (watcher, _, board, target) = owner();
    watcher.set_target_pins(target).unwrap();
    expect_no(&watcher, Duration::from_millis(100), |e| {
        matches!(e, Event::OutputsReset { .. })
    });
    assert_eq!(board.pin_config(), target);
}

#[test]
fn reports_connect_failures_once() {
    let board = FakeBoard::new(SERIAL_A);
    board.set_open_error(Some(io::ErrorKind::PermissionDenied));
    let watcher = start(FakeBus::new(&[&board]), WatchOptions::default());
    let error = await_event!(&watcher, Event::ConnectFailed { error } => error);
    assert!(matches!(&error, Error::Open { source, .. } if source.kind() == io::ErrorKind::PermissionDenied));
    expect_no(&watcher, Duration::from_millis(50), |e| {
        matches!(e, Event::ConnectFailed { .. })
    });

    board.set_open_error(None);
    board.set_protocol_version(2);
    let error = await_event!(&watcher, Event::ConnectFailed { error } => error);
    assert!(matches!(error, Error::ProtocolVersion { device_version: 2 }));
    board.set_protocol_version(1);
    await_event!(&watcher, Event::Connected { .. } => ());
}

#[test]
fn only_reports_failures_that_last() {
    let board = FakeBoard::new(SERIAL_A);
    board.set_open_error(Some(io::ErrorKind::PermissionDenied));
    let watcher = start(
        FakeBus::new(&[&board]),
        WatchOptions {
            scan_interval: Duration::from_millis(200),
            ..Default::default()
        },
    );
    thread::sleep(Duration::from_millis(50)); // one failed try: udev may not have set the permissions yet
    board.set_open_error(None);
    assert!(matches!(next(&watcher), Event::Connected { .. }));
}

/// A bus that fails to list devices until it is told to work.
struct FlakyBus {
    bus: FakeBus,
    failing: Arc<AtomicBool>,
}

impl Bus for FlakyBus {
    fn devices(&self) -> Result<Vec<DeviceEntry>> {
        if self.failing.load(Ordering::SeqCst) {
            return Err(Error::Io(Arc::new(io::Error::other("cannot list /sys/class/hidraw"))));
        }
        self.bus.devices()
    }

    fn open(&self, entry: &DeviceEntry) -> Result<Box<dyn Transport>> {
        self.bus.open(entry)
    }
}

#[test]
fn bus_failures_are_reported_and_retried() {
    let failing = Arc::new(AtomicBool::new(true));
    let bus = FlakyBus {
        bus: FakeBus::new(&[&FakeBoard::new(SERIAL_A)]),
        failing: failing.clone(),
    };
    let watcher = start(bus, WatchOptions::default());
    let error = await_event!(&watcher, Event::ConnectFailed { error } => error);
    assert_eq!(error.to_string(), "cannot list /sys/class/hidraw");
    failing.store(false, Ordering::SeqCst);
    await_event!(&watcher, Event::Connected { .. } => ());
}

#[test]
fn writing_back_is_limited_to_once_a_second() {
    let (watcher, _, board, target) = owner();
    let other = target.with_pin(5, PinSetting::unused()).unwrap();
    board.write_pin_config(&other);
    assert_eq!(
        await_event!(&watcher, Event::PinConfigRestored { count, .. } => count),
        1
    );
    let restored_at = Instant::now();
    board.write_pin_config(&other);
    assert_eq!(
        await_event!(&watcher, Event::PinConfigRestored { count, .. } => count),
        2
    );
    assert!(restored_at.elapsed() >= Duration::from_millis(900));
    assert_eq!(board.pin_config(), target);
}

#[test]
fn methods_can_be_called_while_events_wait() {
    let board = FakeBoard::new(SERIAL_A);
    let watcher = Arc::new(start(
        FakeBus::new(&[&board]),
        WatchOptions {
            target_pins: Some(pins(&[(7, PinSetting::output(false))])),
            ..Default::default()
        },
    ));
    await_event!(&watcher, Event::Connected { .. } => ());
    // More events are waiting to be taken; the watcher must still serve this call.
    let caller = {
        let watcher = watcher.clone();
        thread::spawn(move || watcher.set_outputs([(7, true)]))
    };
    let deadline = Instant::now() + TIMEOUT;
    while !caller.is_finished() {
        assert!(
            Instant::now() < deadline,
            "set_outputs blocked while events were waiting"
        );
        thread::sleep(Duration::from_millis(5));
    }
    caller.join().unwrap().unwrap();
    assert!(board.level(7));
}

#[test]
fn keeps_reading_only_as_fast_as_events_are_taken() {
    let board = FakeBoard::new(SERIAL_A);
    let watcher = start(FakeBus::new(&[&board]), WatchOptions::default());
    await_event!(&watcher, Event::InitialOnOff { .. } => ());
    for i in 0..200 {
        board.set_input(5, i % 2 == 1);
    }
    thread::sleep(Duration::from_millis(200));
    // The application took nothing meanwhile: the watcher stopped reading, and the board kept only
    // as many reports as a hidraw queue holds. Once the queue is read, the next report shows the gap.
    let mut missed = 0;
    let mut take_all = || {
        while let Ok(Some(event)) = watcher.recv_timeout(Duration::from_millis(200)) {
            if let Event::StatusReceived { missed: m, .. } = event {
                missed += m;
            }
        }
    };
    take_all();
    board.periodic();
    take_all();
    assert!(missed > 0);
}

#[test]
fn close_without_taking_events() {
    let board = FakeBoard::new(SERIAL_A);
    let watcher = Arc::new(start(FakeBus::new(&[&board]), WatchOptions::default()));
    let deadline = Instant::now() + TIMEOUT;
    while board.open_count() == 0 {
        assert!(Instant::now() < deadline);
        thread::sleep(Duration::from_millis(1));
    }
    for i in 0..10 {
        board.set_input(5, i % 2 == 1);
    }
    let closer = {
        let watcher = watcher.clone();
        thread::spawn(move || watcher.close())
    };
    while !closer.is_finished() {
        assert!(Instant::now() < deadline + TIMEOUT, "close blocked");
        thread::sleep(Duration::from_millis(5));
    }
    assert!(await_stop(&watcher).is_none());
    assert_eq!(board.open_count(), 0);
    assert!(matches!(
        watcher.set_target_pins(PinConfig::unused()),
        Err(Error::WatcherClosed)
    ));
}

#[test]
fn iterating_ends_when_the_watcher_stops() {
    let (a, b) = (FakeBoard::new(SERIAL_A), FakeBoard::new(SERIAL_B));
    let watcher = start(FakeBus::new(&[&a, &b]), WatchOptions::default());
    assert_eq!(watcher.iter().count(), 0);
    assert!(matches!(watcher.error(), Some(Error::SeveralDevices { .. })));
    assert!(matches!(
        watcher.recv_timeout(Duration::ZERO),
        Err(Error::WatcherClosed)
    ));
}
