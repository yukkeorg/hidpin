"""The Linux hidraw backend: sysfs parsing and ioctl encoding."""

import os
import time

import pytest

from hidpin import _hidraw, device as device_module, protocol
from hidpin.device import HidpinError


HIDPIN_DESCRIPTOR = bytes.fromhex("0600ff0901a101")  # the start of the hidpin report descriptor
KEYBOARD_DESCRIPTOR = bytes.fromhex("05010906a101")


def make_sysfs(
    tmp_path, *, vendor="00001209", product="00006870", serial="DF60BCA003562839", descriptor=HIDPIN_DESCRIPTOR
):
    """Builds the sysfs layout the kernel creates for a USB HID device."""
    usb_device = tmp_path / "devices" / "usb3" / "3-2"
    interface = usb_device / "3-2:1.0"
    hid_device = interface / f"0003:{vendor[-4:]}:{product[-4:]}.000F"
    hid_device.mkdir(parents=True)
    if descriptor is not None:
        (hid_device / "report_descriptor").write_bytes(descriptor)
    (hid_device / "uevent").write_text(
        "DRIVER=hid-generic\n"
        f"HID_ID=0003:{vendor}:{product}\n"
        "HID_NAME=yukke.org hidpin\n"
        f"HID_UNIQ={serial}\n"
    )
    (usb_device / "manufacturer").write_text("yukke.org\n")
    (usb_device / "product").write_text("hidpin\n")
    (usb_device / "serial").write_text(serial + "\n")

    class_dir = tmp_path / "class" / "hidraw" / "hidraw0"
    class_dir.mkdir(parents=True)
    (class_dir / "device").symlink_to(hid_device)
    return tmp_path / "class" / "hidraw"


def test_enumerate_reads_sysfs(tmp_path, monkeypatch):
    monkeypatch.setattr(_hidraw, "SYSFS_HIDRAW", make_sysfs(tmp_path))
    entries = _hidraw.enumerate(0x1209, 0x6870)
    assert len(entries) == 1
    entry = entries[0]
    assert entry["path"] == b"/dev/hidraw0"
    assert entry["vendor_id"] == 0x1209
    assert entry["product_id"] == 0x6870
    assert entry["serial_number"] == "DF60BCA003562839"
    assert entry["manufacturer_string"] == "yukke.org"
    assert entry["product_string"] == "hidpin"


def test_enumerate_filters_by_vendor_and_product(tmp_path, monkeypatch):
    monkeypatch.setattr(_hidraw, "SYSFS_HIDRAW", make_sysfs(tmp_path))
    assert _hidraw.enumerate(0x2E8A, 0x6870) == []
    assert _hidraw.enumerate(0x1209, 0x000A) == []
    assert len(_hidraw.enumerate()) == 1


def test_find_devices_uses_hidraw_entries(tmp_path, monkeypatch):
    monkeypatch.setattr(_hidraw, "SYSFS_HIDRAW", make_sysfs(tmp_path))
    monkeypatch.delenv("HIDPIN_BACKEND", raising=False)
    monkeypatch.setattr(_hidraw, "available", lambda: True)
    entries = device_module.find_devices()
    assert [entry.serial for entry in entries] == ["DF60BCA003562839"]
    assert entries[0].path == b"/dev/hidraw0"


def test_top_level_usage(vectors):
    assert _hidraw.top_level_usage(bytes.fromhex(vectors["report_descriptor"])) == (0xFF00, 0x01)
    assert _hidraw.top_level_usage(KEYBOARD_DESCRIPTOR) == (0x01, 0x06)
    assert _hidraw.top_level_usage(bytes.fromhex("0b010000ffa101")) == (0xFF00, 0x01)  # 4-byte usage
    assert _hidraw.top_level_usage(b"") == (0, 0)
    assert _hidraw.top_level_usage(bytes.fromhex("0600")) == (0, 0)  # cut short


def test_enumerate_reads_the_usage(tmp_path, monkeypatch):
    monkeypatch.setattr(_hidraw, "SYSFS_HIDRAW", make_sysfs(tmp_path))
    entry = _hidraw.enumerate(0x1209, 0x6870)[0]
    assert (entry["usage_page"], entry["usage"]) == (0xFF00, 0x01)


@pytest.mark.parametrize(("descriptor", "found"), [(HIDPIN_DESCRIPTOR, 1), (None, 1), (KEYBOARD_DESCRIPTOR, 0)])
def test_find_devices_checks_the_usage(tmp_path, monkeypatch, descriptor, found):
    monkeypatch.setattr(_hidraw, "SYSFS_HIDRAW", make_sysfs(tmp_path, descriptor=descriptor))
    monkeypatch.delenv("HIDPIN_BACKEND", raising=False)
    monkeypatch.setattr(_hidraw, "available", lambda: True)
    assert len(device_module.find_devices()) == found


def test_ioctl_numbers_match_the_kernel():
    # Values from include/uapi/linux/hidraw.h for a 64 byte buffer.
    assert _hidraw._hidiocsfeature(64) == 0xC0404806
    assert _hidraw._hidiocgfeature(64) == 0xC0404807
    assert _hidraw._hidiocginput(64) == 0xC040480A


def test_backend_selection(monkeypatch):
    monkeypatch.setattr(_hidraw, "available", lambda: True)
    monkeypatch.delenv("HIDPIN_BACKEND", raising=False)
    assert device_module._default_backend() is _hidraw

    monkeypatch.setenv("HIDPIN_BACKEND", "hidraw")
    assert device_module._default_backend() is _hidraw

    monkeypatch.setenv("HIDPIN_BACKEND", "bogus")
    with pytest.raises(HidpinError, match="HIDPIN_BACKEND"):
        device_module._default_backend()

    monkeypatch.setenv("HIDPIN_BACKEND", "hidraw")
    monkeypatch.setattr(_hidraw, "available", lambda: False)
    with pytest.raises(HidpinError, match="hidraw"):
        device_module._default_backend()


def test_hidapi_backend_requested_without_library(monkeypatch):
    monkeypatch.setenv("HIDPIN_BACKEND", "hidapi")

    def no_hid():
        raise HidpinError("hidapi が見つかりません")

    monkeypatch.setattr(device_module, "_import_hid", no_hid)
    with pytest.raises(HidpinError, match="hidapi"):
        device_module._default_backend()


@pytest.fixture
def fifo_device(tmp_path):
    """A hidraw device opened on a FIFO: reads and writes behave like a device node, ioctls fail."""
    path = tmp_path / "hidraw0"
    os.mkfifo(path)
    device = _hidraw.device()
    device.open_path(str(path).encode())
    yield device
    device.close()


def test_read_returns_nothing_when_the_timeout_expires(fifo_device):
    started = time.monotonic()
    assert fifo_device.read(64, timeout_ms=20) == []
    assert time.monotonic() - started >= 0.015


def test_written_report_can_be_read_back(fifo_device):
    report = bytes([protocol.REPORT_ID_OUTPUT]) + protocol.encode_output(1 << 7, 1 << 7)
    assert fifo_device.write(report) == 9
    assert fifo_device.read(64, timeout_ms=100) == list(report)


def test_ioctl_errors_reach_the_caller(fifo_device):
    with pytest.raises(OSError):
        fifo_device.get_feature_report(protocol.REPORT_ID_DEVICE_INFO, 64)
    with pytest.raises(OSError):
        fifo_device.send_feature_report(bytes(64))
    with pytest.raises(OSError):
        fifo_device.get_input_report(protocol.REPORT_ID_STATUS, 64)


def test_a_closed_device_refuses_io(fifo_device):
    fifo_device.close()
    fifo_device.close()  # closing twice is harmless
    with pytest.raises(OSError, match="not open"):
        fifo_device.read(64, timeout_ms=0)
    with pytest.raises(OSError, match="not open"):
        fifo_device.write(b"\x04")
