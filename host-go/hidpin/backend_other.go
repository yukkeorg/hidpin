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
