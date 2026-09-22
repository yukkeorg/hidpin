//go:build linux

package hidpin

import (
	"errors"
	"fmt"
	"os"

	"github.com/yukkeorg/hidpin/host-go/internal/hidraw"
)

// FindDevices lists the connected hidpin devices.
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
		entries = append(entries, Entry{
			Path:         info.Path,
			Serial:       info.Serial,
			Manufacturer: info.Manufacturer,
			Product:      info.Product,
		})
	}
	return entries, nil
}

func openTransport(path string) (Transport, error) {
	transport, err := hidraw.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("cannot open the device (%w). Install the udev rule and replug the "+
				"board: sudo cp udev/70-hidpin.rules /etc/udev/rules.d/ && sudo udevadm control "+
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

func (systemBus) Devices() ([]Entry, error)           { return FindDevices() }
func (systemBus) Open(entry Entry) (Transport, error) { return openTransport(entry.Path) }
