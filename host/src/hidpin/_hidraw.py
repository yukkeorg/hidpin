"""Linux hidraw backend.

Mirrors the small part of the hidapi API that this library uses, but talks to
/dev/hidrawN directly. Unlike the libusb backend of the hidapi wheels it does
not have to detach the kernel driver, so it works with the plain udev rule and
leaves the device usable for other programs.
"""

from __future__ import annotations

import fcntl
import os
import select
from pathlib import Path

SYSFS_HIDRAW = Path("/sys/class/hidraw")
DEV_DIR = Path("/dev")

# Linux ioctl encoding (include/uapi/asm-generic/ioctl.h) for the HIDIOC* requests.
_IOC_WRITE = 1
_IOC_READ = 2
_HID_IOC_TYPE = ord("H")


def _ioc(direction: int, nr: int, size: int) -> int:
    return (direction << 30) | (size << 16) | (_HID_IOC_TYPE << 8) | nr


def _hidiocsfeature(size: int) -> int:
    return _ioc(_IOC_WRITE | _IOC_READ, 0x06, size)


def _hidiocgfeature(size: int) -> int:
    return _ioc(_IOC_WRITE | _IOC_READ, 0x07, size)


def _hidiocginput(size: int) -> int:
    return _ioc(_IOC_WRITE | _IOC_READ, 0x0A, size)


def _read_text(path: Path) -> str:
    try:
        return path.read_text().strip()
    except OSError:
        return ""


def _uevent_fields(path: Path) -> dict[str, str]:
    fields = {}
    for line in _read_text(path).splitlines():
        key, separator, value = line.partition("=")
        if separator:
            fields[key] = value
    return fields


def enumerate(vendor_id: int = 0, product_id: int = 0) -> list[dict]:
    """Lists hidraw devices, in the shape hid.enumerate() returns."""
    results = []
    for node in sorted(SYSFS_HIDRAW.glob("hidraw*")):
        fields = _uevent_fields(node / "device" / "uevent")
        hid_id = fields.get("HID_ID", "").split(":")
        if len(hid_id) != 3:
            continue
        try:
            vid, pid = int(hid_id[1], 16), int(hid_id[2], 16)
        except ValueError:
            continue
        if (vendor_id and vid != vendor_id) or (product_id and pid != product_id):
            continue

        usb_device = (node / "device").resolve().parent.parent
        results.append(
            {
                "path": str(DEV_DIR / node.name).encode(),
                "vendor_id": vid,
                "product_id": pid,
                "serial_number": fields.get("HID_UNIQ", "") or _read_text(usb_device / "serial"),
                "manufacturer_string": _read_text(usb_device / "manufacturer"),
                "product_string": _read_text(usb_device / "product") or fields.get("HID_NAME", ""),
                "usage_page": 0,  # the kernel does not expose it here; the caller checks the reports instead
                "usage": 0,
                "interface_number": -1,
            }
        )
    return results


class device:  # noqa: N801 - named after hid.device so it can stand in for it
    """One open hidraw device."""

    def __init__(self) -> None:
        self._fd: int | None = None

    def open_path(self, path) -> None:
        if isinstance(path, bytes):
            path = path.decode()
        self._fd = os.open(path, os.O_RDWR | os.O_NONBLOCK)

    def _require_fd(self) -> int:
        if self._fd is None:
            raise OSError("device is not open")
        return self._fd

    def read(self, length: int, timeout_ms: int = 0) -> list[int]:
        """Reads one report. timeout_ms < 0 waits forever, 0 returns immediately."""
        fd = self._require_fd()
        timeout = None if timeout_ms < 0 else timeout_ms / 1000.0
        ready, _, _ = select.select([fd], [], [], timeout)
        if not ready:
            return []
        return list(os.read(fd, length))

    def write(self, data) -> int:
        """Sends an output report; the first byte is the report ID."""
        return os.write(self._require_fd(), bytes(data))

    def send_feature_report(self, data) -> int:
        buffer = bytearray(data)
        fcntl.ioctl(self._require_fd(), _hidiocsfeature(len(buffer)), buffer)
        return len(buffer)

    def get_feature_report(self, report_id: int, length: int) -> list[int]:
        buffer = bytearray(length)
        buffer[0] = report_id
        count = fcntl.ioctl(self._require_fd(), _hidiocgfeature(length), buffer)
        return list(buffer[: count if count > 0 else 0])

    def get_input_report(self, report_id: int, length: int) -> list[int]:
        buffer = bytearray(length)
        buffer[0] = report_id
        count = fcntl.ioctl(self._require_fd(), _hidiocginput(length), buffer)
        return list(buffer[: count if count > 0 else 0])

    def close(self) -> None:
        if self._fd is not None:
            os.close(self._fd)
            self._fd = None


def available() -> bool:
    return SYSFS_HIDRAW.is_dir()
