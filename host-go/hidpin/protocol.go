// Package hidpin talks to hidpin devices: RP2040 boards that report the state of their GPIO pins
// over a vendor-defined USB HID interface. The wire format follows docs/PROTOCOL.md in the hidpin
// repository; protocol/vectors.json holds the test vectors shared with the C, Rust and Python code.
package hidpin

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// Protocol constants. Payload lengths exclude the report ID byte.
const (
	ProtocolVersion = 1

	ReportIDStatus     = 0x01
	ReportIDDeviceInfo = 0x02
	ReportIDPinConfig  = 0x03
	ReportIDOutput     = 0x04

	PayloadLen       = 63
	OutputPayloadLen = 8
	// ReportLen is a whole report on the wire: the report ID followed by the payload.
	ReportLen = 1 + PayloadLen

	GPIOCount         = 30
	EventsPerReport   = 7
	DefaultDebounceMS = 20
	ResultGPIONone    = 0xFF
	SaturatedAgeUS    = 0xFFFFFFFF

	statusEventsOffset = 28
	statusEventLen     = 5
	pinConfigOffset    = 3
)

// Reason is the set of reasons a status report was sent (PROTOCOL.md 4.1).
type Reason uint8

const (
	ReasonLevelChanged  Reason = 0x01
	ReasonOutputApplied Reason = 0x02
	ReasonConfigChanged Reason = 0x04
	ReasonPeriodic      Reason = 0x08
	ReasonOutputReset   Reason = 0x10
	ReasonHostRequest   Reason = 0x80
)

var reasonNames = []struct {
	bit  Reason
	name string
}{
	{ReasonLevelChanged, "LEVEL_CHANGED"},
	{ReasonOutputApplied, "OUTPUT_APPLIED"},
	{ReasonConfigChanged, "CONFIG_CHANGED"},
	{ReasonPeriodic, "PERIODIC"},
	{ReasonOutputReset, "OUTPUT_RESET"},
	{ReasonHostRequest, "HOST_REQUEST"},
}

// Names lists the reasons that are set, for example [LEVEL_CHANGED PERIODIC].
func (r Reason) Names() []string {
	names := []string{}
	for _, n := range reasonNames {
		if r&n.bit != 0 {
			names = append(names, n.name)
		}
	}
	return names
}

func (r Reason) String() string {
	if names := r.Names(); len(names) > 0 {
		return strings.Join(names, "|")
	}
	return "0"
}

// StatusFlags are the auxiliary flags of a status report (PROTOCOL.md 4.2).
type StatusFlags uint8

const (
	FlagOverflow   StatusFlags = 0x01
	FlagMoreEvents StatusFlags = 0x02
)

// Names lists the flags that are set.
func (f StatusFlags) Names() []string {
	names := []string{}
	if f&FlagOverflow != 0 {
		names = append(names, "OVERFLOW")
	}
	if f&FlagMoreEvents != 0 {
		names = append(names, "MORE_EVENTS")
	}
	return names
}

func (f StatusFlags) String() string {
	if names := f.Names(); len(names) > 0 {
		return strings.Join(names, "|")
	}
	return "0"
}

// PinMode is how the device uses one GPIO (PROTOCOL.md 6.1).
type PinMode uint8

const (
	ModeUnused   PinMode = 0
	ModeInput    PinMode = 1
	ModePullUp   PinMode = 2
	ModePullDown PinMode = 3
	ModeOutput   PinMode = 4
)

// IsInput reports whether the mode monitors the pin.
func (m PinMode) IsInput() bool {
	return m == ModeInput || m == ModePullUp || m == ModePullDown
}

// Result is the outcome of the last pin configuration write (PROTOCOL.md 6.3).
type Result uint8

const (
	ResultOK              Result = 0
	ResultBadLength       Result = 1
	ResultBadMode         Result = 2
	ResultUnavailableGPIO Result = 3
	ResultUnusedWithParam Result = 4
	ResultBadOutputLevel  Result = 5
)

// Message explains the result in words.
func (r Result) Message() string {
	switch r {
	case ResultOK:
		return "applied"
	case ResultBadLength:
		return "the payload is not 63 bytes long"
	case ResultBadMode:
		return "mode is outside 0-4"
	case ResultUnavailableGPIO:
		return "the pin is not an available GPIO"
	case ResultUnusedWithParam:
		return "an unused pin was given a parameter"
	case ResultBadOutputLevel:
		return "the initial output level is neither 0 nor 1"
	}
	return fmt.Sprintf("result %d", uint8(r))
}

// Board is the board type in the device information (PROTOCOL.md 5.1).
type Board uint8

const (
	BoardPico       Board = 1
	BoardQTPyRP2040 Board = 2
)

// Name is the product name of the board.
func (b Board) Name() string {
	switch b {
	case BoardPico:
		return "Raspberry Pi Pico"
	case BoardQTPyRP2040:
		return "Adafruit QT Py RP2040"
	}
	return fmt.Sprintf("unknown board %d", uint8(b))
}

// MaskToGPIOs lists the GPIO numbers whose bits are set.
func MaskToGPIOs(mask uint32) []int {
	gpios := []int{}
	for gpio := range GPIOCount {
		if mask>>gpio&1 != 0 {
			gpios = append(gpios, gpio)
		}
	}
	return gpios
}

// EdgeEvent is one confirmed change of a pin level (PROTOCOL.md 4.4).
type EdgeEvent struct {
	GPIO  uint8
	Level bool
	AgeUS uint32
}

// Saturated reports whether the change is older than a 32-bit age can express.
func (e EdgeEvent) Saturated() bool {
	return e.AgeUS == SaturatedAgeUS
}

// StartUS is the time the change began, given the timestamp of the report that carried it.
// It returns false when the age is saturated.
func (e EdgeEvent) StartUS(reportTimestampUS uint64) (uint64, bool) {
	if e.Saturated() {
		return 0, false
	}
	return reportTimestampUS - uint64(e.AgeUS), true
}

// Status is a status report, report ID 1 (PROTOCOL.md 4).
type Status struct {
	Seq         uint16
	Reason      Reason
	Flags       StatusFlags
	Monitored   uint32
	Outputs     uint32
	Levels      uint32
	TimestampUS uint64
	Events      []EdgeEvent
}

func (s Status) Overflow() bool   { return s.Flags&FlagOverflow != 0 }
func (s Status) MoreEvents() bool { return s.Flags&FlagMoreEvents != 0 }

// Level is the pin level (monitored pins) or output level (output pins) of a GPIO.
func (s Status) Level(gpio int) bool       { return s.Levels>>gpio&1 != 0 }
func (s Status) IsMonitored(gpio int) bool { return s.Monitored>>gpio&1 != 0 }
func (s Status) IsOutput(gpio int) bool    { return s.Outputs>>gpio&1 != 0 }

// DecodeStatus parses a status report payload.
func DecodeStatus(p []byte) (Status, error) {
	if len(p) != PayloadLen {
		return Status{}, fmt.Errorf("status payload must be %d bytes, got %d", PayloadLen, len(p))
	}
	count := int(p[4])
	if count > EventsPerReport {
		return Status{}, fmt.Errorf("event_count %d exceeds %d", count, EventsPerReport)
	}
	s := Status{
		Seq:         binary.LittleEndian.Uint16(p[0:]),
		Reason:      Reason(p[2]),
		Flags:       StatusFlags(p[3]),
		Monitored:   binary.LittleEndian.Uint32(p[8:]),
		Outputs:     binary.LittleEndian.Uint32(p[12:]),
		Levels:      binary.LittleEndian.Uint32(p[16:]),
		TimestampUS: binary.LittleEndian.Uint64(p[20:]),
		Events:      make([]EdgeEvent, 0, count),
	}
	for i := range count {
		at := statusEventsOffset + statusEventLen*i
		s.Events = append(s.Events, EdgeEvent{
			GPIO:  p[at] & 0x1F,
			Level: p[at]&0x80 != 0,
			AgeUS: binary.LittleEndian.Uint32(p[at+1:]),
		})
	}
	return s, nil
}

// Encode builds the payload of a status report.
func (s Status) Encode() ([]byte, error) {
	if len(s.Events) > EventsPerReport {
		return nil, fmt.Errorf("at most %d events fit in one report", EventsPerReport)
	}
	p := make([]byte, PayloadLen)
	binary.LittleEndian.PutUint16(p[0:], s.Seq)
	p[2] = byte(s.Reason)
	p[3] = byte(s.Flags)
	p[4] = byte(len(s.Events))
	binary.LittleEndian.PutUint32(p[8:], s.Monitored)
	binary.LittleEndian.PutUint32(p[12:], s.Outputs)
	binary.LittleEndian.PutUint32(p[16:], s.Levels)
	binary.LittleEndian.PutUint64(p[20:], s.TimestampUS)
	for i, e := range s.Events {
		at := statusEventsOffset + statusEventLen*i
		p[at] = e.GPIO & 0x1F
		if e.Level {
			p[at] |= 0x80
		}
		binary.LittleEndian.PutUint32(p[at+1:], e.AgeUS)
	}
	return p, nil
}

// DeviceInfo is the device information, report ID 2 (PROTOCOL.md 5).
type DeviceInfo struct {
	ProtocolVersion    uint8
	Firmware           [3]uint8
	Board              Board
	GPIOCount          uint8
	PeriodicIntervalMS uint16
	Available          uint32
	EventsPerReport    uint8
	EventQueueSize     uint8
}

func (i DeviceInfo) BoardName() string { return i.Board.Name() }

func (i DeviceInfo) FirmwareVersion() string {
	return fmt.Sprintf("%d.%d.%d", i.Firmware[0], i.Firmware[1], i.Firmware[2])
}

func (i DeviceInfo) AvailableGPIOs() []int { return MaskToGPIOs(i.Available) }

// DecodeDeviceInfo parses a device information payload.
func DecodeDeviceInfo(p []byte) (DeviceInfo, error) {
	if len(p) != PayloadLen {
		return DeviceInfo{}, fmt.Errorf("device info payload must be %d bytes, got %d", PayloadLen, len(p))
	}
	return DeviceInfo{
		ProtocolVersion:    p[0],
		Firmware:           [3]uint8{p[1], p[2], p[3]},
		Board:              Board(p[4]),
		GPIOCount:          p[5],
		PeriodicIntervalMS: binary.LittleEndian.Uint16(p[6:]),
		Available:          binary.LittleEndian.Uint32(p[8:]),
		EventsPerReport:    p[12],
		EventQueueSize:     p[13],
	}, nil
}

// Encode builds the payload of a device information report.
func (i DeviceInfo) Encode() []byte {
	p := make([]byte, PayloadLen)
	p[0] = i.ProtocolVersion
	copy(p[1:4], i.Firmware[:])
	p[4] = byte(i.Board)
	p[5] = i.GPIOCount
	binary.LittleEndian.PutUint16(p[6:], i.PeriodicIntervalMS)
	binary.LittleEndian.PutUint32(p[8:], i.Available)
	p[12] = i.EventsPerReport
	p[13] = i.EventQueueSize
	return p
}

// PinSetting is how one GPIO is used: a mode plus its parameter (PROTOCOL.md 6.1).
type PinSetting struct {
	Mode  PinMode
	Param uint8
}

// Unused leaves the GPIO alone: input, pulls and output all disabled.
func Unused() PinSetting { return PinSetting{Mode: ModeUnused} }

// MonitorPullUp monitors the GPIO with its pull-up enabled.
func MonitorPullUp(debounceMS uint8) PinSetting { return PinSetting{ModePullUp, debounceMS} }

// MonitorPullDown monitors the GPIO with its pull-down enabled.
func MonitorPullDown(debounceMS uint8) PinSetting { return PinSetting{ModePullDown, debounceMS} }

// MonitorNoPull monitors the GPIO without a pull resistor.
func MonitorNoPull(debounceMS uint8) PinSetting { return PinSetting{ModeInput, debounceMS} }

// Output drives the GPIO, starting from (and returning to) the given initial output level.
func Output(initialHigh bool) PinSetting {
	if initialHigh {
		return PinSetting{ModeOutput, 1}
	}
	return PinSetting{ModeOutput, 0}
}

func (s PinSetting) IsMonitored() bool { return s.Mode.IsInput() }
func (s PinSetting) IsOutput() bool    { return s.Mode == ModeOutput }

// String is the notation the CLI uses: off, nopull:N, pullup:N, pulldown:N, out:low, out:high.
func (s PinSetting) String() string {
	switch s.Mode {
	case ModeUnused:
		return "off"
	case ModeOutput:
		if s.Param != 0 {
			return "out:high"
		}
		return "out:low"
	case ModeInput:
		return fmt.Sprintf("nopull:%d", s.Param)
	case ModePullUp:
		return fmt.Sprintf("pullup:%d", s.Param)
	case ModePullDown:
		return fmt.Sprintf("pulldown:%d", s.Param)
	}
	return fmt.Sprintf("mode%d:%d", uint8(s.Mode), s.Param)
}

// PinConfig is the pin configuration of every GPIO, indexed by GPIO number.
type PinConfig [GPIOCount]PinSetting

// DefaultPinConfig is the configuration the device starts with: every available GPIO monitored
// with its pull-up and the default debounce time.
func DefaultPinConfig(available uint32) PinConfig {
	var c PinConfig
	for gpio := range GPIOCount {
		if available>>gpio&1 != 0 {
			c[gpio] = MonitorPullUp(DefaultDebounceMS)
		}
	}
	return c
}

// WithPins returns a copy with the given GPIOs changed.
func (c PinConfig) WithPins(settings map[int]PinSetting) (PinConfig, error) {
	for gpio, setting := range settings {
		if gpio < 0 || gpio >= GPIOCount {
			return c, fmt.Errorf("GPIO%d is out of range (0-%d)", gpio, GPIOCount-1)
		}
		c[gpio] = setting
	}
	return c, nil
}

func (c PinConfig) mask(match func(PinSetting) bool) uint32 {
	var mask uint32
	for gpio, s := range c {
		if match(s) {
			mask |= 1 << gpio
		}
	}
	return mask
}

func (c PinConfig) MonitoredMask() uint32 { return c.mask(PinSetting.IsMonitored) }
func (c PinConfig) OutputsMask() uint32   { return c.mask(PinSetting.IsOutput) }

// PinConfigReport is the pin configuration report, report ID 3 (PROTOCOL.md 6).
type PinConfigReport struct {
	Result     Result
	ResultGPIO uint8
	RequestID  uint8
	Config     PinConfig
}

// DecodePinConfig parses a pin configuration payload.
func DecodePinConfig(p []byte) (PinConfigReport, error) {
	if len(p) != PayloadLen {
		return PinConfigReport{}, fmt.Errorf("pin config payload must be %d bytes, got %d", PayloadLen, len(p))
	}
	r := PinConfigReport{Result: Result(p[0]), ResultGPIO: p[1], RequestID: p[2]}
	for gpio := range GPIOCount {
		mode := PinMode(p[pinConfigOffset+2*gpio])
		if mode > ModeOutput {
			return PinConfigReport{}, fmt.Errorf("mode %d of GPIO%d is undefined", uint8(mode), gpio)
		}
		r.Config[gpio] = PinSetting{Mode: mode, Param: p[pinConfigOffset+2*gpio+1]}
	}
	return r, nil
}

// Encode builds the payload of a pin configuration report.
func (r PinConfigReport) Encode() []byte {
	p := make([]byte, PayloadLen)
	p[0] = byte(r.Result)
	p[1] = r.ResultGPIO
	p[2] = r.RequestID
	for gpio, s := range r.Config {
		p[pinConfigOffset+2*gpio] = byte(s.Mode)
		p[pinConfigOffset+2*gpio+1] = s.Param
	}
	return p
}

// EncodePinConfigSet builds the payload the host writes; the device ignores the result fields.
func EncodePinConfigSet(c PinConfig, requestID uint8) ([]byte, error) {
	if requestID == 0 {
		return nil, fmt.Errorf("request_id must be between 1 and 255")
	}
	return PinConfigReport{Result: ResultOK, ResultGPIO: ResultGPIONone, RequestID: requestID, Config: c}.Encode(), nil
}

// ValidatePinConfigPayload repeats the device-side validation of a write (PROTOCOL.md 6.3).
// It returns the result, the GPIO it refers to, and the request_id the device records.
func ValidatePinConfigPayload(p []byte, available uint32) (Result, uint8, uint8) {
	var requestID uint8
	if len(p) >= 3 {
		requestID = p[2]
	}
	if len(p) != PayloadLen {
		return ResultBadLength, ResultGPIONone, requestID
	}
	for gpio := range GPIOCount {
		mode := PinMode(p[pinConfigOffset+2*gpio])
		param := p[pinConfigOffset+2*gpio+1]
		switch {
		case mode > ModeOutput:
			return ResultBadMode, uint8(gpio), requestID
		case available>>gpio&1 == 0 && mode != ModeUnused:
			return ResultUnavailableGPIO, uint8(gpio), requestID
		case mode == ModeUnused && param != 0:
			return ResultUnusedWithParam, uint8(gpio), requestID
		case mode == ModeOutput && param > 1:
			return ResultBadOutputLevel, uint8(gpio), requestID
		}
	}
	return ResultOK, ResultGPIONone, requestID
}

// ValidatePinConfig checks a configuration before it is written, so errors surface early.
func ValidatePinConfig(c PinConfig, available uint32) (Result, uint8) {
	result, gpio, _ := ValidatePinConfigPayload(PinConfigReport{Config: c}.Encode(), available)
	return result, gpio
}

// EncodeOutput builds the payload of an output report, report ID 4 (PROTOCOL.md 7.1).
func EncodeOutput(mask, value uint32) []byte {
	p := make([]byte, OutputPayloadLen)
	binary.LittleEndian.PutUint32(p[0:], mask)
	binary.LittleEndian.PutUint32(p[4:], value)
	return p
}

// DecodeOutput parses an output report payload into (mask, value).
func DecodeOutput(p []byte) (uint32, uint32, error) {
	if len(p) != OutputPayloadLen {
		return 0, 0, fmt.Errorf("output payload must be %d bytes, got %d", OutputPayloadLen, len(p))
	}
	return binary.LittleEndian.Uint32(p[0:]), binary.LittleEndian.Uint32(p[4:]), nil
}
