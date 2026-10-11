//go:build linux

package hidraw

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
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
	SysfsRoot = makeSysfs(t, "00001209", "00006870", "DF60BCA003562839")
	infos, err := Enumerate(0x1209, 0x6870)
	if err != nil {
		t.Fatal(err)
	}
	want := Info{Path: "/dev/hidraw0", VendorID: 0x1209, ProductID: 0x6870, Serial: "DF60BCA003562839",
		Manufacturer: "yukke.org", Product: "hidpin"}
	if len(infos) != 1 || infos[0] != want {
		t.Errorf("got %+v", infos)
	}
}

func TestEnumerateFilters(t *testing.T) {
	SysfsRoot = makeSysfs(t, "00001209", "00006870", "X")
	for _, ids := range [][2]uint16{{0x2E8A, 0x6870}, {0x1209, 0x000A}} {
		if infos, _ := Enumerate(ids[0], ids[1]); len(infos) != 0 {
			t.Errorf("%#x:%#x matched %v", ids[0], ids[1], infos)
		}
	}
	if infos, _ := Enumerate(0, 0); len(infos) != 1 {
		t.Errorf("wildcard found %d devices", len(infos))
	}
}

func TestTopLevelUsage(t *testing.T) {
	for _, c := range []struct {
		descriptor     string
		page, usageOut uint16
	}{
		{"0600ff0901a10185010910", 0xFF00, 0x01}, // the start of the hidpin descriptor
		{"05010906a101", 0x01, 0x06},             // a keyboard
		{"0b010000ffa101", 0xFF00, 0x01},         // a 4-byte usage with its page
		{"", 0, 0},
		{"0600", 0, 0}, // cut short
	} {
		descriptor, _ := hex.DecodeString(c.descriptor)
		if page, usage := TopLevelUsage(descriptor); page != c.page || usage != c.usageOut {
			t.Errorf("%s: %#x %#x", c.descriptor, page, usage)
		}
	}
}

func TestEnumerateReadsTheUsage(t *testing.T) {
	SysfsRoot = makeSysfs(t, "00001209", "00006870", "X")
	device, _ := filepath.EvalSymlinks(filepath.Join(SysfsRoot, "hidraw0", "device"))
	if err := os.WriteFile(filepath.Join(device, "report_descriptor"), []byte{0x06, 0x00, 0xFF, 0x09, 0x01, 0xA1, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}
	infos, err := Enumerate(0x1209, 0x6870)
	if err != nil || len(infos) != 1 || infos[0].UsagePage != 0xFF00 || infos[0].Usage != 0x01 {
		t.Errorf("got %+v, %v", infos, err)
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

// openFIFO opens a hidraw Device on a FIFO: reads and writes behave like a device node, ioctls fail.
func openFIFO(t *testing.T) *Device {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hidraw0")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	device, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { device.Close() })
	return device
}

func TestReadReturnsNothingWhenTheTimeoutExpires(t *testing.T) {
	device := openFIFO(t)
	started := time.Now()
	n, err := device.Read(make([]byte, 64), 20*time.Millisecond)
	if n != 0 || err != nil || time.Since(started) < 15*time.Millisecond {
		t.Errorf("read %d, %v after %v", n, err, time.Since(started))
	}
}

func TestWrittenReportCanBeReadBack(t *testing.T) {
	device := openFIFO(t)
	report := []byte{0x04, 0x80, 0, 0, 0, 0x80, 0, 0, 0}
	if n, err := device.Write(report); n != 9 || err != nil {
		t.Fatalf("wrote %d, %v", n, err)
	}
	buf := make([]byte, 64)
	n, err := device.Read(buf, 100*time.Millisecond)
	if err != nil || !bytes.Equal(buf[:n], report) {
		t.Errorf("read %x, %v", buf[:n], err)
	}
}

func TestIoctlErrorsReachTheCaller(t *testing.T) {
	device := openFIFO(t)
	buf := make([]byte, 64)
	buf[0] = 0x02
	if _, err := device.GetFeatureReport(buf); err == nil {
		t.Error("GetFeatureReport on a FIFO succeeded")
	}
	if _, err := device.SendFeatureReport(buf); err == nil {
		t.Error("SendFeatureReport on a FIFO succeeded")
	}
	if _, err := device.GetInputReport(buf); err == nil {
		t.Error("GetInputReport on a FIFO succeeded")
	}
}

func TestAClosedDeviceRefusesIO(t *testing.T) {
	device := openFIFO(t)
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := device.Read(make([]byte, 64), 0); err == nil {
		t.Error("Read after Close succeeded")
	}
	if _, err := device.Write([]byte{0x04}); err == nil {
		t.Error("Write after Close succeeded")
	}
}

func TestOpenMissingNode(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "hidraw9")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("opened a missing node: %v", err)
	}
}
