//! Host library for hidpin devices: RP2040 and RP2350A boards that report the state of their GPIO
//! pins over a vendor-defined USB HID interface.
//!
//! It follows docs/HOST_API.md of the hidpin repository, the API every host library provides, and
//! speaks the wire format of docs/PROTOCOL.md. `Device` talks to one open device; `watch` keeps one
//! device connected for a long-running program and reports what happens as events. The Linux
//! backend reads /dev/hidraw* directly; other systems get `Error::UnsupportedPlatform`.
//!
//! ```no_run
//! use hidpin::{Device, PinSetting};
//!
//! let mut device = Device::open(None)?; // the only connected board
//! device.update_pins([(5, PinSetting::pull_up(20))])?;
//! let config = device.pin_config()?;
//! loop {
//!     if let Some(report) = device.read_status(Some(std::time::Duration::from_secs(1)))? {
//!         println!("{:?}", device.on_off(&report, &config));
//!     }
//! }
//! # Ok::<(), hidpin::Error>(())
//! ```

mod device;
mod error;
#[cfg(target_os = "linux")]
mod hidraw;
pub mod protocol;
pub mod testing;
mod transport;
mod watch;

pub use device::{
    choose_device, find_devices, usb_ids, Device, DeviceEntry, SystemBus, DEFAULT_PRODUCT_ID, DEFAULT_VENDOR_ID,
};
pub use error::{Error, Result};
pub use protocol::{
    Board, Chip, DeviceInfo, EdgeEvent, PinConfig, PinConfigReport, PinMode, PinSetting, Quirks, Reason, ResultCode,
    StatusFlags, StatusReport, PROTOCOL_VERSION,
};
pub use transport::{Bus, Transport};
pub use watch::{watch, Event, OnOffChange, OutputResetCause, WatchOptions, Watcher, DEFAULT_SCAN_INTERVAL};

// Compiles and runs the examples of README.md as doc tests.
#[cfg(doctest)]
#[doc = include_str!("../README.md")]
struct ReadmeDoctests;
