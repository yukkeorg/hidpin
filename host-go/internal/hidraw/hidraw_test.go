//go:build linux

package hidraw

import (
	"os"
	"path/filepath"
	"testing"
)

// makeSysfs builds the layout the kernel creates for a USB HID device and returns the class dir.
func makeSysfs(t *testing.T, vendor, product, serial string) string {
	t.Helper()
	root := t.TempDir()
	usbDevice := filepath.Join(root, "devices", "usb3", "3-2")
	hidDevice := filepath.Join(usbDevice, "3-2:1.0", "0003:"+vendor[4:]+":"+product[4:]+".000F")
	if err := os.MkdirAll(hidDevice, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, text string) {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(hidDevice, "uevent"),
		"DRIVER=hid-generic\nHID_ID=0003:"+vendor+":"+product+"\nHID_NAME=yukke.org hidpin\nHID_UNIQ="+serial+"\n")
	write(filepath.Join(usbDevice, "manufacturer"), "yukke.org\n")
	write(filepath.Join(usbDevice, "product"), "hidpin\n")
	write(filepath.Join(usbDevice, "serial"), serial+"\n")

	class := filepath.Join(root, "class", "hidraw")
	if err := os.MkdirAll(filepath.Join(class, "hidraw0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hidDevice, filepath.Join(class, "hidraw0", "device")); err != nil {
		t.Fatal(err)
	}
	return class
}

func TestEnumerateReadsSysfs(t *testing.T) {
	SysfsRoot = makeSysfs(t, "00001209", "00000001", "DF60BCA003562839")
	infos, err := Enumerate(0x1209, 0x0001)
	if err != nil {
		t.Fatal(err)
	}
	want := Info{Path: "/dev/hidraw0", VendorID: 0x1209, ProductID: 0x0001, Serial: "DF60BCA003562839",
		Manufacturer: "yukke.org", Product: "hidpin"}
	if len(infos) != 1 || infos[0] != want {
		t.Errorf("got %+v", infos)
	}
}

func TestEnumerateFilters(t *testing.T) {
	SysfsRoot = makeSysfs(t, "00001209", "00000001", "X")
	for _, ids := range [][2]uint16{{0x2E8A, 0x0001}, {0x1209, 0x000A}} {
		if infos, _ := Enumerate(ids[0], ids[1]); len(infos) != 0 {
			t.Errorf("%#x:%#x matched %v", ids[0], ids[1], infos)
		}
	}
	if infos, _ := Enumerate(0, 0); len(infos) != 1 {
		t.Errorf("wildcard found %d devices", len(infos))
	}
}

func TestIoctlNumbersMatchTheKernel(t *testing.T) {
	// include/uapi/linux/hidraw.h for a 64-byte buffer.
	for name, got := range map[string]uintptr{
		"HIDIOCSFEATURE": hidiocSFeature(64),
		"HIDIOCGFEATURE": hidiocGFeature(64),
		"HIDIOCGINPUT":   hidiocGInput(64),
	} {
		want := map[string]uintptr{"HIDIOCSFEATURE": 0xC0404806, "HIDIOCGFEATURE": 0xC0404807, "HIDIOCGINPUT": 0xC040480A}[name]
		if got != want {
			t.Errorf("%s = %#x, want %#x", name, got, want)
		}
	}
}
