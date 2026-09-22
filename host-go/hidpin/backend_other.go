//go:build !linux

package hidpin

// FindDevices lists the connected hidpin devices.
func FindDevices() ([]Entry, error) {
	return nil, ErrUnsupportedPlatform
}

// OpenPath opens a device node and checks its protocol version.
func OpenPath(path, serial string) (*Device, error) {
	return nil, ErrUnsupportedPlatform
}

type systemBus struct{}

// SystemBus is the bus of the operating system. This one has no backend: every call fails with
// ErrUnsupportedPlatform.
func SystemBus() Bus { return systemBus{} }

func (systemBus) Devices() ([]Entry, error)           { return nil, ErrUnsupportedPlatform }
func (systemBus) Open(entry Entry) (Transport, error) { return nil, ErrUnsupportedPlatform }
