//! What carries reports to and from a device, and what finds devices.

use std::io;
use std::time::Duration;

use crate::device::DeviceEntry;
use crate::error::Result;

/// Carries reports to and from one device. Every report starts with its report ID. The Linux
/// hidraw backend implements it, and so does the simulated board of `hidpin::testing`; implement it
/// to run a `Device` on a transport of your own.
pub trait Transport: Send {
    /// Waits for one input report. Returns 0 when the timeout expires; `None` waits forever.
    fn read(&mut self, buf: &mut [u8], timeout: Option<Duration>) -> io::Result<usize>;
    /// Sends an output report.
    fn write(&mut self, report: &[u8]) -> io::Result<usize>;
    /// Reads a feature report; `buf[0]` selects the report ID and receives it back.
    fn get_feature_report(&mut self, buf: &mut [u8]) -> io::Result<usize>;
    /// Writes a feature report.
    fn send_feature_report(&mut self, report: &[u8]) -> io::Result<usize>;
    /// Reads an input report on the control pipe; `buf[0]` selects the report ID.
    fn get_input_report(&mut self, buf: &mut [u8]) -> io::Result<usize>;
}

/// Finds and opens hidpin devices: `SystemBus` uses the operating system, and
/// `hidpin::testing::FakeBus` simulates boards being plugged in and out.
pub trait Bus: Send + Sync {
    /// Lists the connected hidpin devices.
    fn devices(&self) -> Result<Vec<DeviceEntry>>;
    /// Opens one of the listed devices.
    fn open(&self, entry: &DeviceEntry) -> Result<Box<dyn Transport>>;
}
