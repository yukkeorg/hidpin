package hidpin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DefaultScanInterval is how often a watcher looks for its device while it is disconnected.
const DefaultScanInterval = time.Second

// ErrDisconnected is returned when an operation needs the device while it is disconnected.
var ErrDisconnected = errors.New("the hidpin device is not connected")

// ErrWatcherClosed is returned by the methods of a watcher that has stopped.
var ErrWatcherClosed = errors.New("the watcher has stopped")

// allGPIOs is the mask of every GPIO number, for checks that do not depend on the board.
const allGPIOs = 1<<GPIOCount - 1

// WatchOptions configures Watch.
type WatchOptions struct {
	// Serial is the serial number of the device to watch. When it is empty, the watcher takes
	// the first device it finds and from then on waits for that one only; it stops with
	// ErrSeveralDevices if it finds more than one at once.
	Serial string
	// TargetPins is the target pin configuration: the watcher writes it whenever the device has
	// another one, after every connection and whenever another program changes it. Build it
	// with PinConfig{}.WithPins; pins it does not set are unused. When it is nil, the watcher
	// never writes the pin configuration and follows the one on the device.
	TargetPins *PinConfig
	// ActiveLow overrides the polarity of some pins: true means LOW is ON. The other monitored
	// pins are active low when they have a pull-up.
	ActiveLow map[int]bool
	// ScanInterval is how often to look for the device while it is disconnected. Zero means
	// DefaultScanInterval.
	ScanInterval time.Duration
	// Bus finds and opens the device. Nil means SystemBus().
	Bus Bus
}

// Watcher keeps one device connected for a long-running program: it waits for the device,
// reopens it after it was unplugged, keeps its pin configuration at the target and reports what
// happens as events. Its methods are safe for concurrent use.
type Watcher struct {
	loop      *watchLoop
	events    chan Event
	requests  chan request
	done      chan struct{}
	finished  chan struct{}
	closeOnce sync.Once

	mu  sync.Mutex
	err error
}

type request struct {
	run    func() error
	result chan error
}

// Watch starts watching a device and returns at once; the device need not be connected yet.
// Watching stops when ctx is cancelled, when Close is called, or when the options turn out to be
// wrong for the device (see Err).
func Watch(ctx context.Context, opts WatchOptions) (*Watcher, error) {
	if opts.ScanInterval < 0 {
		return nil, fmt.Errorf("the scan interval must not be negative: %v", opts.ScanInterval)
	}
	interval := opts.ScanInterval
	if interval == 0 {
		interval = DefaultScanInterval
	}
	var target *PinConfig
	if opts.TargetPins != nil {
		if result, gpio := ValidatePinConfig(*opts.TargetPins, allGPIOs); result != ResultOK {
			return nil, &PinConfigRejectedError{Result: result, GPIO: gpio, Local: true}
		}
		config := *opts.TargetPins
		target = &config
	}
	activeLow := map[int]bool{}
	for gpio, low := range opts.ActiveLow {
		if gpio < 0 || gpio >= GPIOCount {
			return nil, fmt.Errorf("GPIO%d is out of range (0-%d)", gpio, GPIOCount-1)
		}
		activeLow[gpio] = low
	}
	bus := opts.Bus
	if bus == nil {
		if _, _, err := USBIDs(); err != nil {
			return nil, err
		}
		bus = SystemBus()
	}

	w := &Watcher{
		events:   make(chan Event),
		requests: make(chan request),
		done:     make(chan struct{}),
		finished: make(chan struct{}),
	}
	w.loop = &watchLoop{
		w:         w,
		ctx:       ctx,
		bus:       bus,
		serial:    opts.Serial,
		target:    target,
		activeLow: activeLow,
		interval:  interval,
		known:     map[int]bool{},
	}
	go w.loop.run()
	return w, nil
}

// Events delivers the events in the order they happened. It is closed when the watcher stops;
// Err then tells why. While the application does not receive, the watcher stops reading the
// device, and status reports lost that way are reported through StatusReceived.Missed.
func (w *Watcher) Events() <-chan Event {
	return w.events
}

// Err returns the error that stopped the watcher once Events is closed: a *PinConfigRejectedError
// when the target pin configuration does not suit the board, an error wrapping
// ErrSeveralDevices, or ErrUnsupportedPlatform. It is nil when the watcher was stopped by Close
// or its context, and while it is running.
func (w *Watcher) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Close stops the watcher, closes the device and waits until Events is closed.
func (w *Watcher) Close() error {
	w.closeOnce.Do(func() { close(w.done) })
	<-w.finished
	return nil
}

func (w *Watcher) stop(err error) {
	w.mu.Lock()
	w.err = err
	w.mu.Unlock()
	close(w.events)
	close(w.finished)
}

// do runs f on the goroutine that owns the device.
func (w *Watcher) do(f func() error) error {
	req := request{run: f, result: make(chan error, 1)}
	select {
	case w.requests <- req:
	case <-w.finished:
		return ErrWatcherClosed
	}
	return <-req.result
}

// SetOutputs drives output pins, like Device.SetOutputs. It fails with ErrDisconnected while
// the device is disconnected; the levels are not remembered for later.
func (w *Watcher) SetOutputs(levels map[int]bool) error {
	return w.do(func() error {
		if w.loop.conn == nil {
			return ErrDisconnected
		}
		return w.loop.conn.device.SetOutputs(levels)
	})
}

// SetTargetPins replaces the target pin configuration; a watcher without one gets one. It is
// checked against the board when the watcher has connected to it before, and a configuration
// that does not suit the board is refused with *PinConfigRejectedError, leaving the target as it
// was. While the device is connected, the configuration is written at once; if that fails for
// another reason, the new target stays and is written again later. While the device is
// disconnected, it is written on the next connection.
func (w *Watcher) SetTargetPins(config PinConfig) error {
	if result, gpio := ValidatePinConfig(config, allGPIOs); result != ResultOK {
		return &PinConfigRejectedError{Result: result, GPIO: gpio, Local: true}
	}
	return w.do(func() error { return w.loop.setTarget(config) })
}
