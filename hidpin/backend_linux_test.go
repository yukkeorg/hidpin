//go:build linux

package hidpin

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/yukkeorg/hidpin/hidpin/internal/hidraw"
)

// fakeNode is one hidraw node of fakeSysfs. A nil descriptor leaves the report descriptor out, as
// when it cannot be read.
type fakeNode struct {
	serial     string
	descriptor []byte
}

// fakeSysfs builds one hidraw node per device under a temporary sysfs tree and points the hidraw
// package at it.
func fakeSysfs(t *testing.T, devices []fakeNode) {
	t.Helper()
	root := t.TempDir()
	class := filepath.Join(root, "class", "hidraw")
	for i, d := range devices {
		hid := filepath.Join(root, "devices", "usb"+d.serial, "1-1", "1-1:1.0", "0003:1209:6870.0001")
		node := filepath.Join(class, fmt.Sprintf("hidraw%d", i))
		for _, dir := range []string{hid, node} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		uevent := "HID_ID=0003:00001209:00006870\nHID_UNIQ=" + d.serial + "\n"
		if err := os.WriteFile(filepath.Join(hid, "uevent"), []byte(uevent), 0o644); err != nil {
			t.Fatal(err)
		}
		if d.descriptor != nil {
			if err := os.WriteFile(filepath.Join(hid, "report_descriptor"), d.descriptor, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(hid, filepath.Join(node, "device")); err != nil {
			t.Fatal(err)
		}
	}
	saved := hidraw.SysfsRoot
	hidraw.SysfsRoot = class
	t.Cleanup(func() { hidraw.SysfsRoot = saved })
}

func TestFindDevicesChecksTheUsageAndSortsBySerial(t *testing.T) {
	t.Setenv("HIDPIN_VID", "")
	t.Setenv("HIDPIN_PID", "")
	hidpinDescriptor := []byte{0x06, 0x00, 0xFF, 0x09, 0x01, 0xA1, 0x01}
	fakeSysfs(t, []fakeNode{
		{"C", hidpinDescriptor},
		{"K", []byte{0x05, 0x01, 0x09, 0x06, 0xA1, 0x01}}, // another interface: a keyboard
		{"A", nil}, // unreadable descriptor: kept
		{"B", hidpinDescriptor},
	})
	entries, err := FindDevices()
	if err != nil {
		t.Fatal(err)
	}
	var serials []string
	for _, e := range entries {
		serials = append(serials, e.Serial)
	}
	if len(serials) != 3 || serials[0] != "A" || serials[1] != "B" || serials[2] != "C" {
		t.Errorf("found %v", serials)
	}
}
