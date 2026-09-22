package hidpintest

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/yukkeorg/hidpin/host-go/hidpin"
)

// ErrGone is what the transports of an unplugged board return, like ENODEV from hidraw.
var ErrGone = errors.New("the device is gone")

// Bus is a hidpin.Bus that simulates the USB ports of a host, to test code that uses
// hidpin.Watch: boards can be plugged in and out while it runs.
type Bus struct {
	mu     sync.Mutex
	boards []*Board
	nodes  int
}

// NewBus returns a bus with the given boards plugged in.
func NewBus(boards ...*Board) *Bus {
	b := &Bus{}
	for _, board := range boards {
		b.Plug(board)
	}
	return b
}

func (b *Bus) Devices() ([]hidpin.Entry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entries := []hidpin.Entry{}
	for _, board := range b.boards {
		entries = append(entries, hidpin.Entry{Path: board.path, Serial: board.Serial, Manufacturer: "yukke.org", Product: "hidpin"})
	}
	return entries, nil
}

func (b *Bus) Open(entry hidpin.Entry) (hidpin.Transport, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, board := range b.boards {
		if board.path == entry.Path {
			return board.open()
		}
	}
	return nil, fmt.Errorf("cannot open %s: %w", entry.Path, os.ErrNotExist)
}

// Plug connects a board. A board that was unplugged starts afresh, as it lost power.
func (b *Bus) Plug(board *Board) {
	b.mu.Lock()
	defer b.mu.Unlock()
	path := fmt.Sprintf("/dev/hidraw%d", b.nodes)
	b.nodes++
	board.plug(path)
	b.boards = append(b.boards, board)
}

// Unplug disconnects a board: its open transports fail with ErrGone and it loses power.
func (b *Bus) Unplug(board *Board) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.boards = slices.DeleteFunc(b.boards, func(x *Board) bool { return x == board })
	board.unplug()
}

// Board simulates a Raspberry Pi Pico running the hidpin firmware, following docs/PROTOCOL.md
// closely enough for hidpin.Watch. Every GPIO input starts HIGH, like an open switch with a
// pull-up. Status reports go to the transports that are open when they are sent.
type Board struct {
	Serial string

	mu         sync.Mutex
	path       string
	powered    bool
	openErr    error
	info       hidpin.DeviceInfo
	config     hidpin.PinConfig
	result     hidpin.Result
	resultGPIO uint8
	requestID  uint8
	inputs     uint32 // the electrical value of every GPIO
	levels     uint32 // pin levels of monitored pins and output levels of output pins
	nextSeq    uint16
	lastSeq    uint16
	clockUS    uint64
	overflow   bool
	handles    []*handle
}

// NewBoard returns a board that is not plugged in yet.
func NewBoard(serial string) *Board {
	return &Board{
		Serial: serial,
		info: hidpin.DeviceInfo{
			ProtocolVersion: hidpin.ProtocolVersion, Firmware: [3]uint8{0, 1, 0}, Board: hidpin.BoardPico,
			GPIOCount: hidpin.GPIOCount, PeriodicIntervalMS: 1000, Available: PicoAvailable,
			EventsPerReport: hidpin.EventsPerReport, EventQueueSize: 32,
		},
		inputs: PicoAvailable,
	}
}

func (b *Board) plug(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.path = path
	if b.powered {
		return
	}
	b.powered = true
	b.config = hidpin.DefaultPinConfig(b.info.Available)
	b.result, b.resultGPIO, b.requestID = hidpin.ResultOK, hidpin.ResultGPIONone, 0
	b.levels = b.inputs & b.config.MonitoredMask()
	// The host sees the board some seconds after it started.
	b.nextSeq, b.lastSeq, b.clockUS, b.overflow = 0, 0, 5_000_000, false
}

func (b *Board) unplug() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failHandlesLocked()
	b.powered = false
	b.path = ""
}

func (b *Board) failHandlesLocked() {
	for _, h := range b.handles {
		h.fail()
	}
	b.handles = nil
}

func (b *Board) open() (hidpin.Transport, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openErr != nil {
		return nil, b.openErr
	}
	h := &handle{board: b, reports: make(chan []byte, 64), closed: make(chan struct{}), failed: make(chan struct{})}
	b.handles = append(b.handles, h)
	return h, nil
}

// SetOpenError makes opening the board fail with err, for example os.ErrPermission; nil lets it
// open again.
func (b *Board) SetOpenError(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.openErr = err
}

// SetProtocolVersion changes the protocol version the board reports.
func (b *Board) SetProtocolVersion(version uint8) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.info.ProtocolVersion = version
}

// SetInput changes the electrical value of a GPIO. On a monitored pin that changes the pin level
// and sends an edge event, which began the debounce time before it was reported.
func (b *Board) SetInput(gpio int, high bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	bit := uint32(1) << gpio
	b.inputs = b.inputs&^bit | levelBit(gpio, high)
	if !b.powered || !b.config[gpio].IsMonitored() || b.levels&bit == b.inputs&bit {
		return
	}
	b.levels = b.levels&^bit | b.inputs&bit
	age := uint32(b.config[gpio].Param) * 1000
	b.sendLocked(hidpin.ReasonLevelChanged, []hidpin.EdgeEvent{{GPIO: uint8(gpio), Level: high, AgeUS: age}})
}

// LoseInput changes the electrical value of a monitored pin as if its edge event were dropped
// because the event queue was full: the next report carries the new level and OVERFLOW.
func (b *Board) LoseInput(gpio int, high bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	bit := uint32(1) << gpio
	b.inputs = b.inputs&^bit | levelBit(gpio, high)
	if b.config[gpio].IsMonitored() {
		b.levels = b.levels&^bit | b.inputs&bit
		b.overflow = true
	}
}

// Periodic sends a periodic status report.
func (b *Board) Periodic() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sendLocked(hidpin.ReasonPeriodic, nil)
}

// SkipReports sends n status reports that the host never reads.
func (b *Board) SkipReports(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextSeq += uint16(n)
	b.lastSeq = b.nextSeq - 1
}

// Suspend simulates a USB suspend: the outputs go back to their initial output level and, if
// there are any, the next report says OUTPUT_RESET.
func (b *Board) Suspend() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.resetOutputsLocked() {
		b.sendLocked(hidpin.ReasonOutputReset, nil)
	}
}

// Reenumerate simulates a USB reset that makes the host create the device node anew without the
// board losing power: open transports fail, and the outputs go back to their initial output
// level while the pin configuration stays.
func (b *Board) Reenumerate() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failHandlesLocked()
	b.resetOutputsLocked()
}

func (b *Board) resetOutputsLocked() bool {
	outputs := b.config.OutputsMask()
	for _, gpio := range hidpin.MaskToGPIOs(outputs) {
		b.levels = b.levels&^(1<<gpio) | levelBit(gpio, b.config[gpio].Param != 0)
	}
	return outputs != 0
}

// WritePinConfig writes a pin configuration as another program would.
func (b *Board) WritePinConfig(config hidpin.PinConfig) hidpin.Result {
	b.mu.Lock()
	defer b.mu.Unlock()
	payload, _ := hidpin.EncodePinConfigSet(config, 1)
	b.writeConfigLocked(payload)
	return b.result
}

// PinConfig is the pin configuration of the board.
func (b *Board) PinConfig() hidpin.PinConfig {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.config
}

// Level is the pin level or output level of a GPIO.
func (b *Board) Level(gpio int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.levels>>gpio&1 != 0
}

// OpenCount is the number of open transports.
func (b *Board) OpenCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.handles)
}

func levelBit(gpio int, high bool) uint32 {
	if high {
		return 1 << gpio
	}
	return 0
}

func (b *Board) statusLocked(reason hidpin.Reason, events []hidpin.EdgeEvent) hidpin.Status {
	return hidpin.Status{
		Reason:      reason,
		Monitored:   b.config.MonitoredMask(),
		Outputs:     b.config.OutputsMask(),
		Levels:      b.levels,
		TimestampUS: b.clockUS,
		Events:      events,
	}
}

func (b *Board) sendLocked(reason hidpin.Reason, events []hidpin.EdgeEvent) {
	b.clockUS += 1000
	s := b.statusLocked(reason, events)
	s.Seq = b.nextSeq
	if b.overflow {
		s.Flags |= hidpin.FlagOverflow
		b.overflow = false
	}
	b.lastSeq = b.nextSeq
	b.nextSeq++
	payload, err := s.Encode()
	if err != nil {
		panic(err)
	}
	report := append([]byte{hidpin.ReportIDStatus}, payload...)
	for _, h := range b.handles {
		select {
		case h.reports <- report:
		default: // the host did not read in time; like hidraw, drop the report
		}
	}
}

func (b *Board) writeConfigLocked(payload []byte) {
	result, gpio, requestID := hidpin.ValidatePinConfigPayload(payload, b.info.Available)
	b.result, b.resultGPIO, b.requestID = result, gpio, requestID
	if result != hidpin.ResultOK {
		return
	}
	decoded, _ := hidpin.DecodePinConfig(payload)
	next := decoded.Config
	if next == b.config {
		return
	}
	for gpio := range hidpin.GPIOCount {
		prev, setting, bit := b.config[gpio], next[gpio], uint32(1)<<gpio
		switch {
		case prev == setting:
		case prev.IsMonitored() && setting.IsMonitored() && prev.Mode == setting.Mode:
			// only the debounce time changed: the pin level stays
		case setting.IsMonitored():
			b.levels = b.levels&^bit | b.inputs&bit
		case setting.IsOutput():
			b.levels = b.levels&^bit | levelBit(gpio, setting.Param != 0)
		default:
			b.levels &^= bit
		}
	}
	b.config = next
	b.sendLocked(hidpin.ReasonConfigChanged, nil)
}

// handle is one open transport of a board.
type handle struct {
	board     *Board
	reports   chan []byte
	closed    chan struct{}
	closeOnce sync.Once
	failed    chan struct{}
	failOnce  sync.Once
}

func (h *handle) fail() {
	h.failOnce.Do(func() { close(h.failed) })
}

func (h *handle) check() error {
	select {
	case <-h.closed:
		return os.ErrClosed
	case <-h.failed:
		return ErrGone
	default:
		return nil
	}
}

func (h *handle) Read(buf []byte, timeout time.Duration) (int, error) {
	if err := h.check(); err != nil {
		return 0, err
	}
	var expired <-chan time.Time
	if timeout >= 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		expired = timer.C
	}
	select {
	case report := <-h.reports:
		return copy(buf, report), nil
	case <-h.failed:
		return 0, ErrGone
	case <-h.closed:
		return 0, os.ErrClosed
	case <-expired:
		return 0, nil
	}
}

func (h *handle) Write(report []byte) (int, error) {
	if err := h.check(); err != nil {
		return 0, err
	}
	b := h.board
	b.mu.Lock()
	defer b.mu.Unlock()
	if report[0] != hidpin.ReportIDOutput {
		return 0, errors.New("unexpected output report")
	}
	mask, value, err := hidpin.DecodeOutput(report[1:])
	if err != nil {
		return len(report), nil // the firmware ignores it
	}
	mask &= b.config.OutputsMask()
	b.levels = b.levels&^mask | value&mask
	b.sendLocked(hidpin.ReasonOutputApplied, nil)
	return len(report), nil
}

func (h *handle) GetFeatureReport(buf []byte) (int, error) {
	if err := h.check(); err != nil {
		return 0, err
	}
	b := h.board
	b.mu.Lock()
	defer b.mu.Unlock()
	var payload []byte
	switch buf[0] {
	case hidpin.ReportIDDeviceInfo:
		payload = b.info.Encode()
	case hidpin.ReportIDPinConfig:
		payload = hidpin.PinConfigReport{Result: b.result, ResultGPIO: b.resultGPIO, RequestID: b.requestID, Config: b.config}.Encode()
	default:
		return 0, errors.New("unexpected feature report")
	}
	return copy(buf[1:], payload) + 1, nil
}

func (h *handle) SendFeatureReport(report []byte) (int, error) {
	if err := h.check(); err != nil {
		return 0, err
	}
	b := h.board
	b.mu.Lock()
	defer b.mu.Unlock()
	if report[0] != hidpin.ReportIDPinConfig {
		return 0, errors.New("unexpected feature report")
	}
	b.writeConfigLocked(report[1:])
	return len(report), nil
}

func (h *handle) GetInputReport(buf []byte) (int, error) {
	if err := h.check(); err != nil {
		return 0, err
	}
	b := h.board
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.statusLocked(hidpin.ReasonHostRequest, nil)
	s.Seq = b.lastSeq
	payload, _ := s.Encode()
	return copy(buf[1:], payload) + 1, nil
}

func (h *handle) Close() error {
	h.closeOnce.Do(func() { close(h.closed) })
	b := h.board
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handles = slices.DeleteFunc(b.handles, func(x *handle) bool { return x == h })
	return nil
}
