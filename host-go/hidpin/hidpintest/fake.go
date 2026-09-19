// Package hidpintest provides a fake hidpin device for tests. It follows docs/PROTOCOL.md closely
// enough to exercise code written against the hidpin package without any hardware.
package hidpintest

import (
	"errors"
	"time"

	"github.com/yukkeorg/hidpin/host-go/hidpin"
)

// PicoAvailable is the available-GPIO mask of a Raspberry Pi Pico.
const PicoAvailable = 0x1C7FFFFF

// Fake is a hidpin.Transport that behaves like the firmware.
type Fake struct {
	Info       hidpin.DeviceInfo
	Config     hidpin.PinConfig
	Result     hidpin.Result
	ResultGPIO uint8
	RequestID  uint8

	// Conflict makes the device read back a request_id other than the one written, as if another
	// process had written a configuration in between.
	Conflict bool
	// ValidateAvailable, when set, replaces the available mask the device validates against.
	ValidateAvailable *uint32
	// AfterFeatureWrite runs after every pin configuration write.
	AfterFeatureWrite func()
	// OnIdle runs when Read finds no queued report.
	OnIdle func()

	// Reads holds queued input reports (report ID first); Queue appends to it.
	Reads         [][]byte
	FeatureWrites [][]byte
	OutputWrites  [][]byte
	Closed        bool
}

// New returns a fake Raspberry Pi Pico speaking the given protocol version.
func New(protocolVersion uint8) *Fake {
	return &Fake{
		Info: hidpin.DeviceInfo{
			ProtocolVersion: protocolVersion, Firmware: [3]uint8{0, 1, 0}, Board: hidpin.BoardPico,
			GPIOCount: hidpin.GPIOCount, PeriodicIntervalMS: 1000, Available: PicoAvailable,
			EventsPerReport: hidpin.EventsPerReport, EventQueueSize: 32,
		},
		Config:     hidpin.DefaultPinConfig(PicoAvailable),
		ResultGPIO: hidpin.ResultGPIONone,
	}
}

// CurrentStatus is what Get_Report(Input) returns: every monitored pin HIGH.
func (f *Fake) CurrentStatus() hidpin.Status {
	return hidpin.Status{
		Reason:      hidpin.ReasonHostRequest,
		Monitored:   f.Config.MonitoredMask(),
		Outputs:     f.Config.OutputsMask(),
		Levels:      f.Config.MonitoredMask(),
		TimestampUS: 1_000_000,
	}
}

// Queue appends a status report for Read to return.
func (f *Fake) Queue(s hidpin.Status) {
	payload, err := s.Encode()
	if err != nil {
		panic(err)
	}
	f.Reads = append(f.Reads, append([]byte{hidpin.ReportIDStatus}, payload...))
}

func (f *Fake) Read(buf []byte, _ time.Duration) (int, error) {
	if len(f.Reads) == 0 {
		if f.OnIdle != nil {
			f.OnIdle()
		}
		return 0, nil
	}
	n := copy(buf, f.Reads[0])
	f.Reads = f.Reads[1:]
	return n, nil
}

func (f *Fake) Write(report []byte) (int, error) {
	f.OutputWrites = append(f.OutputWrites, append([]byte(nil), report...))
	return len(report), nil
}

func (f *Fake) GetFeatureReport(buf []byte) (int, error) {
	var payload []byte
	switch buf[0] {
	case hidpin.ReportIDDeviceInfo:
		payload = f.Info.Encode()
	case hidpin.ReportIDPinConfig:
		payload = hidpin.PinConfigReport{Result: f.Result, ResultGPIO: f.ResultGPIO, RequestID: f.RequestID, Config: f.Config}.Encode()
	default:
		return 0, errors.New("unexpected feature report")
	}
	return copy(buf[1:], payload) + 1, nil
}

func (f *Fake) SendFeatureReport(report []byte) (int, error) {
	f.FeatureWrites = append(f.FeatureWrites, append([]byte(nil), report...))
	payload := report[1:]
	available := f.Info.Available
	if f.ValidateAvailable != nil {
		available = *f.ValidateAvailable
	}
	result, gpio, requestID := hidpin.ValidatePinConfigPayload(payload, available)
	f.Result, f.ResultGPIO, f.RequestID = result, gpio, requestID
	if f.Conflict {
		f.RequestID = requestID%255 + 1 // always differs from what was sent
	}
	if result == hidpin.ResultOK {
		decoded, _ := hidpin.DecodePinConfig(payload)
		f.Config = decoded.Config
	}
	if f.AfterFeatureWrite != nil {
		f.AfterFeatureWrite()
	}
	return len(report), nil
}

func (f *Fake) GetInputReport(buf []byte) (int, error) {
	payload, _ := f.CurrentStatus().Encode()
	return copy(buf[1:], payload) + 1, nil
}

func (f *Fake) Close() error {
	f.Closed = true
	return nil
}
