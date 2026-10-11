//go:build linux

package hidpin

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/yukkeorg/hidpin/hidpin/internal/hidraw"
)

// The top-level collection of the hidpin interface (PROTOCOL.md 1).
const (
	usagePage = 0xFF00
	usage     = 0x01
)

// FindDevices lists the connected hidpin devices, ordered by serial number. A HID interface whose
// top-level collection is not Usage Page 0xFF00, Usage 0x01 is left out; when the report
// descriptor cannot be read, opening the device checks the protocol version instead.
func FindDevices() ([]Entry, error) {
	vendorID, productID, err := USBIDs()
	if err != nil {
		return nil, err
	}
	infos, err := hidraw.Enumerate(vendorID, productID)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(infos))
	for _, info := range infos {
		if info.UsagePage != 0 && (info.UsagePage != usagePage || info.Usage != usage) {
			continue
		}
		entries = append(entries, Entry{
			Path:         info.Path,
			Serial:       info.Serial,
			Manufacturer: info.Manufacturer,
			Product:      info.Product,
		})
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		return cmp.Or(cmp.Compare(a.Serial, b.Serial), cmp.Compare(a.Path, b.Path))
	})
	return entries, nil
}

func openTransport(path string) (Transport, error) {
	transport, err := hidraw.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("cannot open the device (%w). Install the udev rule and replug the "+
				"board: sudo cp udev/60-hidpin.rules /etc/udev/rules.d/ && sudo udevadm control "+
				"--reload-rules && sudo udevadm trigger", err)
		}
		return nil, fmt.Errorf("cannot open the device: %w", err)
	}
	return transport, nil
}

// OpenPath opens a device node such as /dev/hidraw0 and checks its protocol version.
func OpenPath(path, serial string) (*Device, error) {
	transport, err := openTransport(path)
	if err != nil {
		return nil, err
	}
	device := NewDevice(transport, serial)
	if _, err := device.Info(); err != nil {
		transport.Close()
		return nil, err
	}
	return device, nil
}

type systemBus struct{}

// SystemBus is the bus of the operating system: /dev/hidraw* on Linux.
func SystemBus() Bus { return systemBus{} }

func (systemBus) Devices() ([]Entry, error) {
	return FindDevices()
}

func (systemBus) Open(entry Entry) (Transport, error) {
	return openTransport(entry.Path)
}
