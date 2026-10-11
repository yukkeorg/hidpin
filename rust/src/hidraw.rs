//! The Linux backend: hidraw device nodes, found through sysfs.

use std::fs::{self, File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::fd::AsRawFd;
use std::path::{Path, PathBuf};
use std::time::Duration;

use crate::transport::Transport;

/// One hidraw node found in sysfs.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct Info {
    pub path: String,
    pub vendor_id: u16,
    pub product_id: u16,
    pub serial: String,
    pub manufacturer: String,
    pub product: String,
    /// Those of the first application collection; both 0 when the descriptor cannot be read.
    pub usage_page: u16,
    pub usage: u16,
}

fn read_text(path: &Path) -> String {
    fs::read_to_string(path)
        .map(|s| s.trim().to_string())
        .unwrap_or_default()
}

fn uevent_field(text: &str, key: &str) -> Option<String> {
    text.lines().find_map(|line| {
        line.strip_prefix(key)
            .and_then(|rest| rest.strip_prefix('='))
            .map(str::to_string)
    })
}

/// The Usage Page and Usage of the first application collection of a report descriptor, or 0 and
/// 0 when it has none.
pub(crate) fn top_level_usage(descriptor: &[u8]) -> (u16, u16) {
    let (mut usage_page, mut usage) = (0u16, 0u16);
    let mut i = 0;
    while i < descriptor.len() {
        let prefix = descriptor[i];
        if prefix == 0xFE {
            // A long item: the data size is in the next byte.
            match descriptor.get(i + 1) {
                Some(&size) => i += 3 + usize::from(size),
                None => break,
            }
            continue;
        }
        let size = [0, 1, 2, 4][usize::from(prefix & 0x03)];
        let mut data = 0u32;
        for k in 0..size {
            if let Some(&byte) = descriptor.get(i + 1 + k) {
                data |= u32::from(byte) << (8 * k);
            }
        }
        i += 1 + size;
        match prefix & 0xFC {
            0x04 => usage_page = data as u16, // Usage Page (global)
            0x08 => {
                // Usage (local); a 4-byte usage carries its page in the upper half.
                if size == 4 {
                    usage_page = (data >> 16) as u16;
                }
                usage = data as u16;
            }
            0xA0 if data == 0x01 => return (usage_page, usage), // Collection (Application)
            _ => {}
        }
    }
    (0, 0)
}

/// Lists the hidraw nodes under `sysfs_root` (normally /sys/class/hidraw), naming them under
/// `dev_dir`; a zero vendor or product id matches any.
pub(crate) fn enumerate_in(
    sysfs_root: &Path,
    dev_dir: &Path,
    vendor_id: u16,
    product_id: u16,
) -> io::Result<Vec<Info>> {
    let mut infos = Vec::new();
    for entry in fs::read_dir(sysfs_root)? {
        let entry = entry?;
        let name = entry.file_name().to_string_lossy().into_owned();
        if !name.starts_with("hidraw") {
            continue;
        }
        let device = entry.path().join("device");
        let uevent = read_text(&device.join("uevent"));
        let Some(hid_id) = uevent_field(&uevent, "HID_ID") else {
            continue;
        };
        let ids: Vec<&str> = hid_id.split(':').collect();
        if ids.len() != 3 {
            continue;
        }
        let (Ok(vid), Ok(pid)) = (u32::from_str_radix(ids[1], 16), u32::from_str_radix(ids[2], 16)) else {
            continue;
        };
        let (vid, pid) = (vid as u16, pid as u16);
        if (vendor_id != 0 && vid != vendor_id) || (product_id != 0 && pid != product_id) {
            continue;
        }
        // .../3-2/3-2:1.0/0003:1209:6870.000F -> the USB device is two levels up.
        let usb_device: PathBuf = fs::canonicalize(&device)
            .ok()
            .and_then(|p| p.parent().and_then(Path::parent).map(Path::to_path_buf))
            .unwrap_or_default();
        let mut serial = uevent_field(&uevent, "HID_UNIQ").unwrap_or_default();
        if serial.is_empty() {
            serial = read_text(&usb_device.join("serial"));
        }
        let mut product = read_text(&usb_device.join("product"));
        if product.is_empty() {
            product = uevent_field(&uevent, "HID_NAME").unwrap_or_default();
        }
        let descriptor = fs::read(device.join("report_descriptor")).unwrap_or_default();
        let (usage_page, usage) = top_level_usage(&descriptor);
        infos.push(Info {
            path: dev_dir.join(&name).to_string_lossy().into_owned(),
            vendor_id: vid,
            product_id: pid,
            serial,
            manufacturer: read_text(&usb_device.join("manufacturer")),
            product,
            usage_page,
            usage,
        });
    }
    infos.sort_by(|a, b| a.path.cmp(&b.path));
    Ok(infos)
}

// Linux ioctl encoding (include/uapi/asm-generic/ioctl.h) for the HIDIOC* requests.
const IOC_WRITE: u64 = 1;
const IOC_READ: u64 = 2;

fn hid_ioc(nr: u64, size: usize) -> u64 {
    ((IOC_WRITE | IOC_READ) << 30) | ((size as u64) << 16) | (u64::from(b'H') << 8) | nr
}

pub(crate) fn hidioc_s_feature(size: usize) -> u64 {
    hid_ioc(0x06, size)
}

pub(crate) fn hidioc_g_feature(size: usize) -> u64 {
    hid_ioc(0x07, size)
}

pub(crate) fn hidioc_g_input(size: usize) -> u64 {
    hid_ioc(0x0A, size)
}

/// One open hidraw node.
pub(crate) struct HidrawDevice {
    file: File,
}

impl HidrawDevice {
    pub fn open(path: &str) -> io::Result<HidrawDevice> {
        Ok(HidrawDevice {
            file: OpenOptions::new().read(true).write(true).open(path)?,
        })
    }

    fn ioctl(&self, request: u64, buf: &mut [u8]) -> io::Result<usize> {
        // SAFETY: buf is valid for its whole length, which the request encodes as its size.
        let result = unsafe { libc::ioctl(self.file.as_raw_fd(), request as _, buf.as_mut_ptr()) };
        if result < 0 {
            return Err(io::Error::last_os_error());
        }
        Ok(result as usize)
    }
}

impl Transport for HidrawDevice {
    fn read(&mut self, buf: &mut [u8], timeout: Option<Duration>) -> io::Result<usize> {
        let mut fds = libc::pollfd {
            fd: self.file.as_raw_fd(),
            events: libc::POLLIN,
            revents: 0,
        };
        let millis = match timeout {
            None => -1,
            Some(d) => d.as_millis().min(i32::MAX as u128) as i32,
        };
        // SAFETY: fds is one valid pollfd.
        let ready = unsafe { libc::poll(&mut fds, 1, millis) };
        if ready < 0 {
            return Err(io::Error::last_os_error());
        }
        if ready == 0 {
            return Ok(0);
        }
        self.file.read(buf)
    }

    fn write(&mut self, report: &[u8]) -> io::Result<usize> {
        self.file.write(report)
    }

    fn get_feature_report(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        self.ioctl(hidioc_g_feature(buf.len()), buf)
    }

    fn send_feature_report(&mut self, report: &[u8]) -> io::Result<usize> {
        let mut buf = report.to_vec();
        self.ioctl(hidioc_s_feature(buf.len()), &mut buf)
    }

    fn get_input_report(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        self.ioctl(hidioc_g_input(buf.len()), buf)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ioctl_numbers_match_the_kernel() {
        // include/uapi/linux/hidraw.h for a 64-byte buffer.
        assert_eq!(hidioc_s_feature(64), 0xC040_4806);
        assert_eq!(hidioc_g_feature(64), 0xC040_4807);
        assert_eq!(hidioc_g_input(64), 0xC040_480A);
    }

    #[test]
    fn top_level_usage_of_descriptors() {
        let cases: [(&[u8], (u16, u16)); 5] = [
            (&[0x06, 0x00, 0xFF, 0x09, 0x01, 0xA1, 0x01, 0x85, 0x01], (0xFF00, 0x01)), // hidpin
            (&[0x05, 0x01, 0x09, 0x06, 0xA1, 0x01], (0x01, 0x06)),                     // a keyboard
            (&[0x0B, 0x01, 0x00, 0x00, 0xFF, 0xA1, 0x01], (0xFF00, 0x01)),             // a 4-byte usage
            (&[], (0, 0)),
            (&[0x06, 0x00], (0, 0)), // cut short
        ];
        for (descriptor, want) in cases {
            assert_eq!(top_level_usage(descriptor), want, "{descriptor:02x?}");
        }
    }

    /// Builds the layout the kernel creates for one USB HID device and returns the class directory.
    fn make_sysfs(root: &Path, serial: &str, descriptor: Option<&[u8]>) -> PathBuf {
        let usb_device = root.join("devices/usb3/3-2");
        let hid_device = usb_device.join("3-2:1.0/0003:1209:6870.000F");
        fs::create_dir_all(&hid_device).unwrap();
        fs::write(
            hid_device.join("uevent"),
            format!(
                "DRIVER=hid-generic\nHID_ID=0003:00001209:00006870\nHID_NAME=yukke.org hidpin\nHID_UNIQ={serial}\n"
            ),
        )
        .unwrap();
        if let Some(descriptor) = descriptor {
            fs::write(hid_device.join("report_descriptor"), descriptor).unwrap();
        }
        fs::write(usb_device.join("manufacturer"), "yukke.org\n").unwrap();
        fs::write(usb_device.join("product"), "hidpin\n").unwrap();
        let class = root.join("class/hidraw");
        fs::create_dir_all(class.join("hidraw0")).unwrap();
        std::os::unix::fs::symlink(&hid_device, class.join("hidraw0/device")).unwrap();
        class
    }

    fn temp_dir(name: &str) -> PathBuf {
        let dir = std::env::temp_dir().join(format!("hidpin-rs-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        dir
    }

    #[test]
    fn enumerate_reads_sysfs() {
        let root = temp_dir("sysfs");
        let class = make_sysfs(
            &root,
            "DF60BCA003562839",
            Some(&[0x06, 0x00, 0xFF, 0x09, 0x01, 0xA1, 0x01]),
        );
        let infos = enumerate_in(&class, Path::new("/dev"), 0x1209, 0x6870).unwrap();
        assert_eq!(
            infos,
            vec![Info {
                path: "/dev/hidraw0".into(),
                vendor_id: 0x1209,
                product_id: 0x6870,
                serial: "DF60BCA003562839".into(),
                manufacturer: "yukke.org".into(),
                product: "hidpin".into(),
                usage_page: 0xFF00,
                usage: 0x01,
            }]
        );
        assert!(enumerate_in(&class, Path::new("/dev"), 0x2E8A, 0x6870)
            .unwrap()
            .is_empty());
        assert!(enumerate_in(&class, Path::new("/dev"), 0x1209, 0x000A)
            .unwrap()
            .is_empty());
        assert_eq!(enumerate_in(&class, Path::new("/dev"), 0, 0).unwrap().len(), 1);
        fs::remove_dir_all(root).unwrap();
    }

    /// A hidraw device opened on a FIFO: reads and writes behave like a device node, ioctls fail.
    fn open_fifo(name: &str) -> (HidrawDevice, PathBuf) {
        let dir = temp_dir(name);
        let path = dir.join("hidraw0");
        let c_path = std::ffi::CString::new(path.to_str().unwrap()).unwrap();
        // SAFETY: c_path is a valid C string.
        assert_eq!(unsafe { libc::mkfifo(c_path.as_ptr(), 0o600) }, 0);
        (HidrawDevice::open(path.to_str().unwrap()).unwrap(), dir)
    }

    #[test]
    fn read_returns_nothing_when_the_timeout_expires() {
        let (mut device, dir) = open_fifo("timeout");
        let started = std::time::Instant::now();
        assert_eq!(device.read(&mut [0u8; 64], Some(Duration::from_millis(20))).unwrap(), 0);
        assert!(started.elapsed() >= Duration::from_millis(15));
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn written_report_can_be_read_back() {
        let (mut device, dir) = open_fifo("echo");
        let report = [0x04, 0x80, 0, 0, 0, 0x80, 0, 0, 0];
        assert_eq!(device.write(&report).unwrap(), 9);
        let mut buf = [0u8; 64];
        let n = device.read(&mut buf, Some(Duration::from_millis(100))).unwrap();
        assert_eq!(&buf[..n], &report);
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn ioctl_errors_reach_the_caller() {
        let (mut device, dir) = open_fifo("ioctl");
        let mut buf = [0u8; 64];
        buf[0] = 0x02;
        assert!(device.get_feature_report(&mut buf).is_err());
        assert!(device.send_feature_report(&buf).is_err());
        assert!(device.get_input_report(&mut buf).is_err());
        fs::remove_dir_all(dir).unwrap();
    }
}
