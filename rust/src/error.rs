//! The errors of the library (docs/HOST_API.md 8).

use std::fmt;
use std::io;
use std::sync::Arc;

use crate::protocol::{ResultCode, RESULT_GPIO_NONE};

/// The result type of the library.
pub type Result<T> = std::result::Result<T, Error>;

/// Every error of the library. The kinds of docs/HOST_API.md 8 are variants of their own, so a
/// caller tells them apart with `match`.
#[derive(Debug, Clone)]
pub enum Error {
    /// No device matches: `serial` is the serial number asked for, if any.
    DeviceNotFound { serial: Option<String> },
    /// No serial number was given, but several devices are connected.
    SeveralDevices { serials: Vec<String> },
    /// The device speaks another protocol version.
    ProtocolVersion { device_version: u8 },
    /// A pin configuration is invalid. `local` is true when the library caught it before anything
    /// was sent to the device.
    PinConfigRejected { result: ResultCode, gpio: u8, local: bool },
    /// Another process wrote the pin configuration in between (PROTOCOL.md 6.3).
    PinConfigConflict { expected: u8, seen: u8 },
    /// The device applied another configuration than the one sent.
    PinConfigMismatch,
    /// There is no backend on this operating system.
    UnsupportedPlatform,
    /// A Watch operation needs the device while it is disconnected.
    DeviceDisconnected,
    /// A Watch operation after the watcher stopped.
    WatcherClosed,
    /// A report from the device does not have the expected shape.
    Malformed(String),
    /// An argument is out of range or malformed.
    InvalidArgument(String),
    /// Opening a device node failed.
    Open { path: String, source: Arc<io::Error> },
    /// Another error of the operating system or the transport.
    Io(Arc<io::Error>),
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Error::DeviceNotFound { serial: None } => write!(f, "no hidpin device found"),
            Error::DeviceNotFound { serial: Some(serial) } => {
                write!(f, "no hidpin device found: no device with serial number {serial}")
            }
            Error::SeveralDevices { serials } => {
                write!(
                    f,
                    "several devices are connected; pick one with --serial: {}",
                    serials.join(", ")
                )
            }
            Error::ProtocolVersion { device_version } => write!(
                f,
                "device speaks protocol version {device_version}, this library speaks {}",
                crate::protocol::PROTOCOL_VERSION
            ),
            Error::PinConfigRejected { result, gpio, local } => {
                if *local {
                    write!(f, "the pin configuration is invalid (nothing was sent to the device): ")?;
                } else {
                    write!(f, "the device rejected the pin configuration: ")?;
                }
                write!(f, "{}", result.message())?;
                if *gpio != RESULT_GPIO_NONE {
                    write!(f, " (GPIO{gpio})")?;
                }
                Ok(())
            }
            Error::PinConfigConflict { expected, seen } => write!(
                f,
                "another process changed the pin configuration (sent request_id {expected}, read back {seen})"
            ),
            Error::PinConfigMismatch => {
                write!(
                    f,
                    "the configuration the device applied differs from the one that was sent"
                )
            }
            Error::UnsupportedPlatform => write!(f, "hidpin supports Linux (hidraw) only"),
            Error::DeviceDisconnected => write!(f, "the hidpin device is not connected"),
            Error::WatcherClosed => write!(f, "the watcher has stopped"),
            Error::Malformed(message) | Error::InvalidArgument(message) => write!(f, "{message}"),
            Error::Open { path, source } => {
                write!(f, "cannot open {path} ({source})")?;
                if source.kind() == io::ErrorKind::PermissionDenied {
                    write!(
                        f,
                        ". Install the udev rule and replug the board: sudo cp udev/60-hidpin.rules \
                         /etc/udev/rules.d/ && sudo udevadm control --reload-rules && sudo udevadm trigger"
                    )?;
                }
                Ok(())
            }
            Error::Io(source) => write!(f, "{source}"),
        }
    }
}

impl std::error::Error for Error {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Error::Open { source, .. } | Error::Io(source) => Some(source.as_ref()),
            _ => None,
        }
    }
}

impl From<io::Error> for Error {
    fn from(error: io::Error) -> Self {
        Error::Io(Arc::new(error))
    }
}
