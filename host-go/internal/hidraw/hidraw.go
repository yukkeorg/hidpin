//go:build linux

// Package hidraw talks to Linux /dev/hidrawN devices directly: enumeration through sysfs and
// reports through read, write and the HIDIOC* ioctls. It needs neither cgo nor hidapi, and unlike
// the libusb backend of hidapi it leaves the kernel driver attached.
package hidraw

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// SysfsRoot and DevDir are variables so tests can point them at a fake tree.
var (
	SysfsRoot = "/sys/class/hidraw"
	DevDir    = "/dev"
)

// Info describes one hidraw device.
type Info struct {
	Path         string
	VendorID     uint16
	ProductID    uint16
	Serial       string
	Manufacturer string
	Product      string
}

func readText(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func ueventFields(path string) map[string]string {
	fields := map[string]string{}
	for _, line := range strings.Split(readText(path), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			fields[key] = value
		}
	}
	return fields
}

// Enumerate lists hidraw devices; a zero vendorID or productID matches any.
func Enumerate(vendorID, productID uint16) ([]Info, error) {
	entries, err := os.ReadDir(SysfsRoot)
	if err != nil {
		return nil, fmt.Errorf("cannot list %s: %w", SysfsRoot, err)
	}
	var infos []Info
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "hidraw") {
			continue
		}
		device := filepath.Join(SysfsRoot, name, "device")
		fields := ueventFields(filepath.Join(device, "uevent"))
		ids := strings.Split(fields["HID_ID"], ":")
		if len(ids) != 3 {
			continue
		}
		vid, err1 := strconv.ParseUint(ids[1], 16, 32)
		pid, err2 := strconv.ParseUint(ids[2], 16, 32)
		if err1 != nil || err2 != nil {
			continue
		}
		if (vendorID != 0 && uint16(vid) != vendorID) || (productID != 0 && uint16(pid) != productID) {
			continue
		}

		// .../3-2/3-2:1.0/0003:1209:0001.000F -> the USB device is two levels up.
		usbDevice := ""
		if resolved, err := filepath.EvalSymlinks(device); err == nil {
			usbDevice = filepath.Dir(filepath.Dir(resolved))
		}
		serial := fields["HID_UNIQ"]
		if serial == "" && usbDevice != "" {
			serial = readText(filepath.Join(usbDevice, "serial"))
		}
		product := readText(filepath.Join(usbDevice, "product"))
		if product == "" {
			product = fields["HID_NAME"]
		}
		infos = append(infos, Info{
			Path:         filepath.Join(DevDir, name),
			VendorID:     uint16(vid),
			ProductID:    uint16(pid),
			Serial:       serial,
			Manufacturer: readText(filepath.Join(usbDevice, "manufacturer")),
			Product:      product,
		})
	}
	sort.Slice(infos, func(a, b int) bool { return infos[a].Path < infos[b].Path })
	return infos, nil
}

// Linux ioctl encoding (include/uapi/asm-generic/ioctl.h) for the HIDIOC* requests.
const (
	iocWrite = 1
	iocRead  = 2
)

func hidIOC(dir, nr, size uintptr) uintptr {
	return dir<<30 | size<<16 | uintptr('H')<<8 | nr
}

func hidiocSFeature(size int) uintptr { return hidIOC(iocWrite|iocRead, 0x06, uintptr(size)) }
func hidiocGFeature(size int) uintptr { return hidIOC(iocWrite|iocRead, 0x07, uintptr(size)) }
func hidiocGInput(size int) uintptr   { return hidIOC(iocWrite|iocRead, 0x0A, uintptr(size)) }

// Device is one open hidraw device.
type Device struct {
	file *os.File
}

// Open opens a hidraw device node such as /dev/hidraw0.
func Open(path string) (*Device, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	return &Device{file: file}, nil
}

// Read reads one input report (report ID first). A negative timeout waits forever; it returns
// 0 and no error when the timeout expires.
func (d *Device) Read(buf []byte, timeout time.Duration) (int, error) {
	deadline := time.Time{}
	if timeout >= 0 {
		deadline = time.Now().Add(timeout)
	}
	if err := d.file.SetReadDeadline(deadline); err != nil {
		return 0, err
	}
	n, err := d.file.Read(buf)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return 0, nil
	}
	return n, err
}

// Write sends an output report; the first byte is the report ID.
func (d *Device) Write(report []byte) (int, error) {
	return d.file.Write(report)
}

func (d *Device) ioctl(request uintptr, buf []byte) (int, error) {
	conn, err := d.file.SyscallConn()
	if err != nil {
		return 0, err
	}
	var result uintptr
	var errno syscall.Errno
	err = conn.Control(func(fd uintptr) {
		result, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(unsafe.Pointer(&buf[0])))
	})
	if err != nil {
		return 0, err
	}
	if errno != 0 {
		return 0, errno
	}
	return int(result), nil
}

// GetFeatureReport reads a feature report; buf[0] selects the report ID and receives it back.
func (d *Device) GetFeatureReport(buf []byte) (int, error) {
	return d.ioctl(hidiocGFeature(len(buf)), buf)
}

// SendFeatureReport writes a feature report; the first byte is the report ID.
func (d *Device) SendFeatureReport(report []byte) (int, error) {
	buf := append([]byte(nil), report...)
	return d.ioctl(hidiocSFeature(len(buf)), buf)
}

// GetInputReport reads an input report on the control pipe; buf[0] selects the report ID.
func (d *Device) GetInputReport(buf []byte) (int, error) {
	return d.ioctl(hidiocGInput(len(buf)), buf)
}

// Close closes the device node.
func (d *Device) Close() error {
	return d.file.Close()
}
