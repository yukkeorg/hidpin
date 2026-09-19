package hidpin

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"
)

// USB identifiers. The defaults are the pid.codes test PID; override them with HIDPIN_VID and
// HIDPIN_PID when the firmware was built with other ids.
const (
	DefaultVendorID  uint16 = 0x1209
	DefaultProductID uint16 = 0x0001
)

// USBIDs returns the vendor and product id to look for.
func USBIDs() (vendorID, productID uint16, err error) {
	if vendorID, err = envID("HIDPIN_VID", DefaultVendorID); err != nil {
		return 0, 0, err
	}
	if productID, err = envID("HIDPIN_PID", DefaultProductID); err != nil {
		return 0, 0, err
	}
	return vendorID, productID, nil
}

func envID(name string, fallback uint16) (uint16, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseUint(raw, 0, 64)
	if err != nil {
		return 0, fmt.Errorf("%s is not a 16-bit number: '%s'", name, raw)
	}
	if value > 0xFFFF {
		return 0, fmt.Errorf("%s is out of range for 16 bits: '%s'", name, raw)
	}
	return uint16(value), nil
}

// Transport carries reports to and from one device. Every report starts with its report ID.
// The Linux hidraw backend implements it; tests use a fake.
type Transport interface {
	// Read waits for one input report. It returns 0 and no error when the timeout expires; a
	// negative timeout waits forever.
	Read(buf []byte, timeout time.Duration) (int, error)
	// Write sends an output report.
	Write(report []byte) (int, error)
	// GetFeatureReport reads a feature report; buf[0] selects the report ID.
	GetFeatureReport(buf []byte) (int, error)
	// SendFeatureReport writes a feature report.
	SendFeatureReport(report []byte) (int, error)
	// GetInputReport reads an input report on the control pipe; buf[0] selects the report ID.
	GetInputReport(buf []byte) (int, error)
	Close() error
}

// Entry is one hidpin device found on the host.
type Entry struct {
	Path         string
	Serial       string
	Manufacturer string
	Product      string
}

// ErrNotFound is returned when no matching device is connected.
var ErrNotFound = errors.New("no hidpin device found")

// ErrUnsupportedPlatform is returned on operating systems without a backend.
var ErrUnsupportedPlatform = errors.New("hidpin supports Linux (hidraw) only")

// ProtocolVersionError means the device speaks a protocol version this library does not.
type ProtocolVersionError struct {
	DeviceVersion uint8
}

func (e *ProtocolVersionError) Error() string {
	return fmt.Sprintf("device speaks protocol version %d, this library speaks %d", e.DeviceVersion, ProtocolVersion)
}

// PinConfigRejectedError means a pin configuration was invalid. Local is true when the library
// caught it before anything was sent to the device.
type PinConfigRejectedError struct {
	Result Result
	GPIO   uint8
	Local  bool
}

func (e *PinConfigRejectedError) Error() string {
	where := "the device rejected the pin configuration"
	if e.Local {
		where = "the pin configuration is invalid (nothing was sent to the device)"
	}
	message := e.Result.Message()
	if e.GPIO != ResultGPIONone {
		message = fmt.Sprintf("%s (GPIO%d)", message, e.GPIO)
	}
	return where + ": " + message
}

// PinConfigConflictError means another process changed the pin configuration at the same time
// (PROTOCOL.md 6.3).
type PinConfigConflictError struct {
	Expected uint8
	Seen     uint8
}

func (e *PinConfigConflictError) Error() string {
	return fmt.Sprintf("another process changed the pin configuration (sent request_id %d, read back %d)", e.Expected, e.Seen)
}

// Open opens the connected device with the given serial number, or the only connected device
// when serial is empty.
func Open(serial string) (*Device, error) {
	entries, err := FindDevices()
	if err != nil {
		return nil, err
	}
	if serial != "" {
		matching := entries[:0]
		for _, e := range entries {
			if e.Serial == serial {
				matching = append(matching, e)
			}
		}
		entries = matching
	}
	switch {
	case len(entries) == 0 && serial == "":
		return nil, ErrNotFound
	case len(entries) == 0:
		return nil, fmt.Errorf("%w: no device with serial number %s", ErrNotFound, serial)
	case len(entries) > 1 && serial == "":
		serials := make([]string, len(entries))
		for i, e := range entries {
			serials[i] = e.Serial
		}
		return nil, fmt.Errorf("several devices are connected; pick one with --serial: %s", strings.Join(serials, ", "))
	}
	return OpenPath(entries[0].Path, entries[0].Serial)
}

// Device is a connected hidpin device. It is not safe for concurrent use.
type Device struct {
	transport Transport
	// Serial is the USB serial number the device was opened by.
	Serial string

	info          *DeviceInfo
	config        *PinConfig
	polarity      map[int]bool
	lastRequestID uint8
	lastSeq       uint16
	haveSeq       bool
	missed        int
}

// NewDevice wraps a transport; the protocol version is checked on the first call to Info.
func NewDevice(transport Transport, serial string) *Device {
	return &Device{transport: transport, Serial: serial, polarity: map[int]bool{}}
}

// Close closes the transport.
func (d *Device) Close() error {
	return d.transport.Close()
}

func (d *Device) getFeature(reportID uint8) ([]byte, error) {
	buf := make([]byte, ReportLen)
	buf[0] = reportID
	n, err := d.transport.GetFeatureReport(buf)
	if err != nil {
		return nil, fmt.Errorf("reading feature report %d: %w", reportID, err)
	}
	if n < ReportLen {
		return nil, fmt.Errorf("feature report %d is only %d bytes long", reportID, n)
	}
	if buf[0] != reportID {
		return nil, fmt.Errorf("asked for feature report %d but got report %d", reportID, buf[0])
	}
	return buf[1:ReportLen], nil
}

// Info returns the device information, reading it once and checking the protocol version.
func (d *Device) Info() (DeviceInfo, error) {
	if d.info != nil {
		return *d.info, nil
	}
	payload, err := d.getFeature(ReportIDDeviceInfo)
	if err != nil {
		return DeviceInfo{}, err
	}
	info, err := DecodeDeviceInfo(payload)
	if err != nil {
		return DeviceInfo{}, err
	}
	if info.ProtocolVersion != ProtocolVersion {
		return DeviceInfo{}, &ProtocolVersionError{DeviceVersion: info.ProtocolVersion}
	}
	d.info = &info
	return info, nil
}

// GetPinConfig reads the pin configuration together with the result of the last write.
func (d *Device) GetPinConfig() (PinConfigReport, error) {
	payload, err := d.getFeature(ReportIDPinConfig)
	if err != nil {
		return PinConfigReport{}, err
	}
	report, err := DecodePinConfig(payload)
	if err != nil {
		return PinConfigReport{}, err
	}
	d.config = &report.Config
	return report, nil
}

// PinConfig returns the pin configuration, reading it only if it has not been read yet.
func (d *Device) PinConfig() (PinConfig, error) {
	if d.config == nil {
		if _, err := d.GetPinConfig(); err != nil {
			return PinConfig{}, err
		}
	}
	return *d.config, nil
}

func (d *Device) nextRequestID() uint8 {
	for {
		id := uint8(rand.IntN(255) + 1)
		if id != d.lastRequestID {
			d.lastRequestID = id
			return id
		}
	}
}

// SetPinConfig writes a pin configuration and verifies it was applied (PROTOCOL.md 6.3).
func (d *Device) SetPinConfig(config PinConfig) (PinConfigReport, error) {
	info, err := d.Info()
	if err != nil {
		return PinConfigReport{}, err
	}
	if result, gpio := ValidatePinConfig(config, info.Available); result != ResultOK {
		return PinConfigReport{}, &PinConfigRejectedError{Result: result, GPIO: gpio, Local: true}
	}

	requestID := d.nextRequestID()
	payload, err := EncodePinConfigSet(config, requestID)
	if err != nil {
		return PinConfigReport{}, err
	}
	if _, err := d.transport.SendFeatureReport(append([]byte{ReportIDPinConfig}, payload...)); err != nil {
		return PinConfigReport{}, fmt.Errorf("writing the pin configuration: %w", err)
	}

	report, err := d.GetPinConfig()
	if err != nil {
		return PinConfigReport{}, err
	}
	if report.RequestID != requestID {
		return PinConfigReport{}, &PinConfigConflictError{Expected: requestID, Seen: report.RequestID}
	}
	if report.Result != ResultOK {
		return PinConfigReport{}, &PinConfigRejectedError{Result: report.Result, GPIO: report.ResultGPIO}
	}
	if report.Config != config {
		return PinConfigReport{}, errors.New("the configuration the device applied differs from the one that was sent")
	}
	return report, nil
}

// UpdatePins changes some pins and keeps every other pin as it is.
func (d *Device) UpdatePins(settings map[int]PinSetting) (PinConfigReport, error) {
	current, err := d.GetPinConfig()
	if err != nil {
		return PinConfigReport{}, err
	}
	config, err := current.Config.WithPins(settings)
	if err != nil {
		return PinConfigReport{}, err
	}
	return d.SetPinConfig(config)
}

// RequestStatus asks for the current state with Get_Report(Input) (PROTOCOL.md 4.6).
func (d *Device) RequestStatus() (Status, error) {
	buf := make([]byte, ReportLen)
	buf[0] = ReportIDStatus
	n, err := d.transport.GetInputReport(buf)
	if err != nil {
		return Status{}, fmt.Errorf("could not read the status report: %w", err)
	}
	if n < ReportLen || buf[0] != ReportIDStatus {
		return Status{}, errors.New("could not read the status report")
	}
	return DecodeStatus(buf[1:ReportLen])
}

// ReadStatus waits for the next status report. It returns false when the timeout expires.
func (d *Device) ReadStatus(timeout time.Duration) (Status, bool, error) {
	buf := make([]byte, ReportLen)
	n, err := d.transport.Read(buf, timeout)
	if err != nil {
		return Status{}, false, err
	}
	if n == 0 {
		return Status{}, false, nil
	}
	if buf[0] != ReportIDStatus {
		return Status{}, false, fmt.Errorf("received an unexpected report ID %d", buf[0])
	}
	if n < ReportLen {
		return Status{}, false, fmt.Errorf("status report is only %d bytes long", n)
	}
	status, err := DecodeStatus(buf[1:ReportLen])
	if err != nil {
		return Status{}, false, err
	}
	d.trackSeq(status)
	return status, true, nil
}

func (d *Device) trackSeq(s Status) {
	if s.Reason&ReasonHostRequest != 0 {
		return
	}
	if d.haveSeq {
		d.missed += int(s.Seq - d.lastSeq - 1)
	}
	d.lastSeq = s.Seq
	d.haveSeq = true
}

// MissedReports counts status reports the host did not read, detected from gaps in seq.
func (d *Device) MissedReports() int {
	return d.missed
}

// WriteOutputs drives the output pins in mask to the levels in value.
func (d *Device) WriteOutputs(mask, value uint32) error {
	_, err := d.transport.Write(append([]byte{ReportIDOutput}, EncodeOutput(mask, value)...))
	return err
}

// SetOutputs drives output pins; GPIOs that are not output pins are ignored by the device.
func (d *Device) SetOutputs(levels map[int]bool) error {
	var mask, value uint32
	for gpio, high := range levels {
		if gpio < 0 || gpio >= GPIOCount {
			return fmt.Errorf("GPIO%d is out of range (0-%d)", gpio, GPIOCount-1)
		}
		mask |= 1 << gpio
		if high {
			value |= 1 << gpio
		}
	}
	return d.WriteOutputs(mask, value)
}

// SetPolarity overrides which level counts as ON for one pin.
func (d *Device) SetPolarity(gpio int, activeLow bool) {
	d.polarity[gpio] = activeLow
}

// IsActiveLow reports whether LOW means ON: true for pins with a pull-up, unless overridden.
func (d *Device) IsActiveLow(gpio int, config PinConfig) bool {
	if activeLow, ok := d.polarity[gpio]; ok {
		return activeLow
	}
	return config[gpio].Mode == ModePullUp
}

// OnOff interprets the pin levels of the monitored pins as ON/OFF.
func (d *Device) OnOff(s Status, config PinConfig) map[int]bool {
	states := map[int]bool{}
	for _, gpio := range MaskToGPIOs(s.Monitored) {
		states[gpio] = s.Level(gpio) != d.IsActiveLow(gpio, config)
	}
	return states
}
