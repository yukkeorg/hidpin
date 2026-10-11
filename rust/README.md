# hidpin for Rust

A Rust host library for hidpin devices, following the API that every host library provides
([docs/HOST_API.md](../docs/HOST_API.md)). It speaks the protocol in
[docs/PROTOCOL.md](../docs/PROTOCOL.md) and is tested against the same
[`protocol/vectors.json`](../protocol/vectors.json) as the C, Python and Go code.

- **Linux only.** It talks to `/dev/hidraw*` directly, with no hidapi; the only dependency is
  `libc`. On other systems `find_devices` and `Device::open` return `Error::UnsupportedPlatform`.
- Crate `hidpin` in this directory, Rust 1.75 or newer. Both layers of the API: `Device` (the core)
  and `watch` (the Watch layer for long-running programs).
- There is no command-line tool in Rust; use the Python or Go one.

| Module | Contents |
|---|---|
| `hidpin` | `Device`, `find_devices`, `choose_device`, `watch` and its `Event`s, `Error` |
| `hidpin::protocol` | constants, report types, encoding, decoding and validation |
| `hidpin::testing` | a simulated bus of boards (`FakeBus`, `FakeBoard`), to test code without hardware |

Add it from a checkout:

```toml
[dependencies]
hidpin = { path = "../hidpin/rust" }
```

## A device

```rust,no_run
use std::time::Duration;

use hidpin::{Device, PinSetting};

fn main() -> hidpin::Result<()> {
    let mut device = Device::open(None)?; // the only connected board; Some("SERIAL") picks one
    println!("{}, {} GPIOs", device.info()?.board_name(), device.info()?.available_gpios().len());

    device.update_pins([(5, PinSetting::pull_up(20)), (7, PinSetting::output(false))])?;
    device.set_outputs([(7, true)])?;
    let config = device.pin_config()?;
    loop {
        let Some(report) = device.read_status(Some(Duration::from_secs(1)))? else {
            continue;
        };
        for event in &report.events {
            println!("GPIO{} {} at {:?} us", event.gpio, event.level, event.start_us(report.timestamp_us));
        }
        println!("{:?}", device.on_off(&report, &config)); // {5: false, ...}
    }
}
```

Errors are one enum, `hidpin::Error`; match on `Error::DeviceNotFound`, `Error::SeveralDevices`,
`Error::ProtocolVersion`, `Error::PinConfigRejected` and so on. The library does not warn about
RP2350 erratum E9: check `info.pull_down_unreliable()` and `config.pull_down_gpios()` yourself.

## Watching for a long time

`Device` stops working when the board is unplugged. `watch` keeps one board connected instead: it
waits for the board, reopens it after it was unplugged, keeps the pin configuration at a target, and
reports what happens as events. A thread of its own owns the device; take the events from any thread.

```rust,no_run
use hidpin::{watch, Event, PinConfig, PinSetting, WatchOptions};

fn main() -> hidpin::Result<()> {
    let target = PinConfig::unused().with_pins([(5, PinSetting::pull_up(20)), (7, PinSetting::output(false))])?;
    let watcher = watch(WatchOptions { target_pins: Some(target), ..Default::default() })?;
    for event in &watcher {
        match event {
            Event::OnOffChange(change) => println!("GPIO{} {} at {:?}", change.gpio, change.on, change.time),
            Event::Connected { .. } => watcher.set_outputs([(7, true)])?,
            Event::Disconnected { .. } => println!("unplugged; waiting for the board"),
            _ => {}
        }
    }
    match watcher.error() {
        Some(error) => Err(error),
        None => Ok(()),
    }
}
```

`recv_timeout` takes one event with a time limit, and `close` (or dropping the watcher) stops it.
Output levels are never restored by the watcher: after `Event::OutputsReset` the outputs are at their
initial output level, and driving them again is up to the application.

## Testing without hardware

```rust
use std::sync::Arc;
use std::time::Duration;

use hidpin::testing::{FakeBoard, FakeBus};
use hidpin::{watch, Event, WatchOptions};

let board = FakeBoard::new("ABCD0123456789EF");
let bus = FakeBus::new(&[&board]);
let watcher = watch(WatchOptions {
    bus: Some(Arc::new(bus.clone())),
    scan_interval: Duration::from_millis(10),
    ..Default::default()
})?;
while !matches!(watcher.recv(), Some(Event::InitialOnOff { .. })) {}
board.set_input(5, false); // press the switch on GPIO5
let change = loop {
    if let Some(Event::OnOffChange(change)) = watcher.recv() {
        break change;
    }
};
assert!(change.gpio == 5 && change.on);
bus.unplug(&board); // bus.plug(&board) brings it back
# Ok::<(), hidpin::Error>(())
```

## Development

```sh
cargo test
cargo clippy --all-targets -- -D warnings
cargo fmt --check
```

Non-root access needs the udev rule (`sudo cp udev/60-hidpin.rules /etc/udev/rules.d/`, then replug
the board). A firmware built with other USB ids is found with `HIDPIN_VID` / `HIDPIN_PID`.
