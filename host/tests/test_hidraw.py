"""The Linux hidraw backend: sysfs parsing and ioctl encoding."""

import pytest

from hidpin import _hidraw, device as device_module
from hidpin.device import HidpinError


def make_sysfs(tmp_path, *, vendor="00001209", product="00000001", serial="DF60BCA003562839"):
    """Builds the sysfs layout the kernel creates for a USB HID device."""
    usb_device = tmp_path / "devices" / "usb3" / "3-2"
    interface = usb_device / "3-2:1.0"
    hid_device = interface / f"0003:{vendor[-4:]}:{product[-4:]}.000F"
    hid_device.mkdir(parents=True)
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
    entries = _hidraw.enumerate(0x1209, 0x0001)
    assert len(entries) == 1
    entry = entries[0]
    assert entry["path"] == b"/dev/hidraw0"
    assert entry["vendor_id"] == 0x1209
    assert entry["product_id"] == 0x0001
    assert entry["serial_number"] == "DF60BCA003562839"
    assert entry["manufacturer_string"] == "yukke.org"
    assert entry["product_string"] == "hidpin"


def test_enumerate_filters_by_vendor_and_product(tmp_path, monkeypatch):
    monkeypatch.setattr(_hidraw, "SYSFS_HIDRAW", make_sysfs(tmp_path))
    assert _hidraw.enumerate(0x2E8A, 0x0001) == []
    assert _hidraw.enumerate(0x1209, 0x000A) == []
    assert len(_hidraw.enumerate()) == 1


def test_find_devices_uses_hidraw_entries(tmp_path, monkeypatch):
    monkeypatch.setattr(_hidraw, "SYSFS_HIDRAW", make_sysfs(tmp_path))
    monkeypatch.delenv("HIDPIN_BACKEND", raising=False)
    monkeypatch.setattr(_hidraw, "available", lambda: True)
    entries = device_module.find_devices()
    assert [entry.serial for entry in entries] == ["DF60BCA003562839"]
    assert entries[0].path == b"/dev/hidraw0"


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
