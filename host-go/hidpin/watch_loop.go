package hidpin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	// readPollInterval bounds how long one Read waits, so that a reader notices it should stop
	// even with a transport whose Close does not interrupt Read.
	readPollInterval = 250 * time.Millisecond
	// restoreInterval is the shortest time between two writes of the target pin configuration
	// over a change made by another program, so that two watchers cannot fight at full speed.
	restoreInterval = time.Second
	// settleTimeout is how long to wait for CONFIG_CHANGED after writing the pin configuration
	// before asking the device for its state instead.
	settleTimeout = time.Second
	// clockWindow is how long one clock sample is trusted (see deviceClock).
	clockWindow = 30 * time.Second
)

type readResult struct {
	report []byte
	at     time.Time
	err    error
}

// connection is one open device, from opening it until it is lost or closed.
type connection struct {
	device    *Device
	transport Transport
	reports   chan readResult
	stop      chan struct{}
	clock     deviceClock

	lastSeq uint16
	haveSeq bool
	// snapshotSeq is the seq of the status requested after connecting. Interrupt reports up to
	// it were sent before that status, which already reflects them.
	snapshotSeq  uint16
	haveSnapshot bool
	// awaitingConfig is set from writing the pin configuration until the device reports
	// CONFIG_CHANGED; reports sent before that still show the old configuration (PROTOCOL.md 6.4).
	awaitingConfig bool
}

// fatalError marks an error that stops the watcher.
type fatalError struct{ err error }

func (e fatalError) Error() string { return e.err.Error() }

// watchLoop is the state of a Watcher. Only its own goroutine touches it; the methods of Watcher
// reach it through requests.
type watchLoop struct {
	w         *Watcher
	ctx       context.Context
	bus       Bus
	serial    string
	target    *PinConfig
	activeLow map[int]bool
	interval  time.Duration

	info          *DeviceInfo
	config        PinConfig    // the pin configuration of the device, as last seen
	known         map[int]bool // the ON/OFF of monitored pins, kept across connections
	everConnected bool
	conn          *connection
	readers       sync.WaitGroup
	pending       []Event

	failure  string
	failures int

	restoreCount int
	lastRestore  time.Time

	scan, restore, settle alarm
}

func (l *watchLoop) run() {
	err := l.loop()
	l.drop()
	l.readers.Wait()
	l.scan.stop()
	if err != nil {
		l.flush()
	}
	l.w.stop(err)
}

func (l *watchLoop) loop() error {
	l.scan.set(0)
	for {
		// Deliver pending events before reading more from the device, but keep serving requests
		// meanwhile: the application may call a method from the goroutine that receives events.
		var out chan<- Event
		var next Event
		var reports <-chan readResult
		if len(l.pending) > 0 {
			out, next = l.w.events, l.pending[0]
		} else if l.conn != nil {
			reports = l.conn.reports
		}
		var err error
		select {
		case <-l.ctx.Done():
			return nil
		case <-l.w.done:
			return nil
		case out <- next:
			l.pending[0] = nil
			l.pending = l.pending[1:]
		case req := <-l.w.requests:
			req.result <- req.run()
		case r := <-reports:
			err = l.received(r)
		case <-l.scan.C():
			l.scan.fired()
			err = l.tryConnect()
		case <-l.restore.C():
			l.restore.fired()
			err = l.restoreTarget()
		case <-l.settle.C():
			l.settle.fired()
			if l.conn != nil {
				l.conn.awaitingConfig = false
				err = l.snapshot()
			}
		}
		if err != nil {
			return err
		}
	}
}

// flush delivers the events that are still pending after a fatal error.
func (l *watchLoop) flush() {
	for len(l.pending) > 0 {
		select {
		case l.w.events <- l.pending[0]:
			l.pending = l.pending[1:]
		case req := <-l.w.requests:
			req.result <- ErrWatcherClosed
		case <-l.w.done:
			return
		case <-l.ctx.Done():
			return
		}
	}
}

func (l *watchLoop) emit(event Event) {
	l.pending = append(l.pending, event)
}

// failed notes a failed attempt to use the device. A cause is reported when it happens twice in a
// row, which skips the moment after plugging in when udev has not set the permissions yet.
func (l *watchLoop) failed(err error) {
	if err.Error() != l.failure {
		l.failure, l.failures = err.Error(), 0
	}
	l.failures++
	if l.failures == 2 {
		l.emit(ConnectFailed{Err: err})
	}
}

func (l *watchLoop) resetFailures() {
	l.failure, l.failures = "", 0
}

func (l *watchLoop) tryConnect() error {
	entries, err := l.bus.Devices()
	if errors.Is(err, ErrUnsupportedPlatform) {
		return err
	}
	if err != nil {
		l.failed(err)
		l.scan.set(l.interval)
		return nil
	}
	var matching []Entry
	for _, e := range entries {
		if l.serial == "" || e.Serial == l.serial {
			matching = append(matching, e)
		}
	}
	switch {
	case len(matching) == 0:
		l.resetFailures()
		l.scan.set(l.interval)
		return nil
	case l.serial == "" && len(matching) > 1:
		return fmt.Errorf("%w; set the serial number of the one to watch: %s", ErrSeveralDevices, joinSerials(matching))
	}
	l.serial = matching[0].Serial
	err = l.connect(matching[0])
	var fatal fatalError
	if errors.As(err, &fatal) {
		return fatal.err
	}
	if err != nil {
		l.failed(err)
		l.scan.set(l.interval)
	}
	return nil
}

func (l *watchLoop) connect(entry Entry) error {
	transport, err := l.bus.Open(entry)
	if err != nil {
		return err
	}
	device := NewDevice(transport, entry.Serial)
	connected := false
	defer func() {
		if !connected {
			transport.Close()
		}
	}()
	info, err := device.Info()
	if err != nil {
		return err
	}
	l.info = &info
	report, err := device.GetPinConfig()
	if err != nil {
		return err
	}
	found := report.Config
	c := &connection{device: device, transport: transport, reports: make(chan readResult), stop: make(chan struct{})}
	var driven []int
	if l.target != nil && found != *l.target {
		if _, err := device.SetPinConfig(*l.target); err != nil {
			var rejected *PinConfigRejectedError
			if errors.As(err, &rejected) {
				return fatalError{err}
			}
			return err
		}
		driven = drivenOutputs(found, *l.target)
		found = *l.target
		c.awaitingConfig = true
	}

	connected = true
	l.conn = c
	l.resetFailures()
	reconnected := l.everConnected
	l.everConnected = true
	l.setConfig(found)
	l.readers.Add(1)
	go l.read(c)

	l.emit(Connected{Serial: entry.Serial, Path: entry.Path, Info: info, PinConfig: found, Reconnected: reconnected})
	// Losing the connection always sends the outputs back to their initial level (PROTOCOL.md 7.2).
	if outputs := MaskToGPIOs(found.OutputsMask()); reconnected && len(outputs) > 0 {
		l.emit(OutputsReset{GPIOs: outputs, Cause: OutputResetReconnect})
	} else if len(driven) > 0 {
		l.emit(OutputsReset{GPIOs: driven, Cause: OutputResetPinConfig})
	}
	if c.awaitingConfig {
		l.settle.set(settleTimeout)
		return nil
	}
	return l.snapshot()
}

func (l *watchLoop) read(c *connection) {
	defer l.readers.Done()
	for {
		buf := make([]byte, ReportLen)
		n, err := c.transport.Read(buf, readPollInterval)
		at := time.Now()
		if err == nil && n == 0 {
			select {
			case <-c.stop:
				return
			default:
				continue
			}
		}
		select {
		case c.reports <- readResult{report: buf[:n], at: at, err: err}:
		case <-c.stop:
			return
		}
		if err != nil {
			return
		}
	}
}

// drop closes the connection, if there is one.
func (l *watchLoop) drop() {
	c := l.conn
	if c == nil {
		return
	}
	l.conn = nil
	close(c.stop)
	c.transport.Close()
	l.restore.stop()
	l.settle.stop()
}

// lost handles a connection that failed, usually because the device was unplugged.
func (l *watchLoop) lost(err error) {
	if l.conn == nil {
		return
	}
	l.drop()
	l.emit(Disconnected{Err: err})
	l.scan.set(l.interval)
}

func (l *watchLoop) received(r readResult) error {
	if r.err != nil {
		l.lost(r.err)
		return nil
	}
	if len(r.report) < ReportLen || r.report[0] != ReportIDStatus {
		return nil
	}
	s, err := DecodeStatus(r.report[1:ReportLen])
	if err != nil {
		return nil
	}
	c := l.conn
	missed := 0
	if c.haveSeq {
		missed = int(s.Seq - c.lastSeq - 1)
	}
	c.lastSeq, c.haveSeq = s.Seq, true
	return l.status(s, r.at, missed)
}

// snapshot asks the device for its current state (Get_Report(Input)) and takes it as the
// reference for later reports.
func (l *watchLoop) snapshot() error {
	c := l.conn
	s, err := c.device.RequestStatus()
	if err != nil {
		l.lost(err)
		return nil
	}
	// With edge events still queued, the levels are ahead of the interrupt reports that will
	// carry those events; learn from the interrupt reports instead, so no event counts twice.
	c.snapshotSeq, c.haveSnapshot = s.Seq, s.Seq != 0 && !s.MoreEvents()
	return l.status(s, time.Now(), 0)
}

func (l *watchLoop) status(s Status, at time.Time, missed int) error {
	c := l.conn
	c.clock.add(at, s.TimestampUS)
	l.emit(StatusReceived{Status: s, Missed: missed, On: l.onOff(s)})
	if s.Reason&ReasonOutputReset != 0 {
		if outputs := MaskToGPIOs(s.Outputs); len(outputs) > 0 {
			l.emit(OutputsReset{GPIOs: outputs, Cause: OutputResetSuspend})
		}
	}
	hostRequest := s.Reason&ReasonHostRequest != 0
	stale := false
	if !hostRequest && c.haveSnapshot {
		// seq wraps around, so compare the distance.
		if int16(s.Seq-c.snapshotSeq) <= 0 {
			stale = true
		} else {
			c.haveSnapshot = false
		}
	}
	if s.Reason&ReasonConfigChanged != 0 {
		if !stale {
			c.awaitingConfig = false
			l.settle.stop()
		}
		if err := l.configChanged(); err != nil {
			return err
		}
		if l.conn == nil {
			return nil
		}
	}
	if stale || c.awaitingConfig || (hostRequest && s.MoreEvents()) {
		return nil
	}
	l.absorb(s)
	return nil
}

// absorb updates the known ON/OFF from a status report and reports what changed.
func (l *watchLoop) absorb(s Status) {
	c := l.conn
	for _, e := range s.Events {
		gpio := int(e.GPIO)
		was, known := l.known[gpio]
		if !known {
			continue
		}
		on := e.Level != l.isActiveLow(gpio)
		if on == was {
			// An edge event always leaves the level before it, so the opposite change was lost.
			l.emit(OnOffChange{GPIO: gpio, On: !on, Level: !e.Level, Inferred: true})
		}
		change := OnOffChange{GPIO: gpio, On: on, Level: e.Level}
		if start, ok := e.StartUS(s.TimestampUS); ok {
			change.HasTime, change.Time, change.DeviceUS = true, c.clock.at(start), start
		}
		l.emit(change)
		l.known[gpio] = on
	}
	learned := map[int]bool{}
	for _, gpio := range MaskToGPIOs(s.Monitored) {
		on := s.Level(gpio) != l.isActiveLow(gpio)
		if was, known := l.known[gpio]; !known {
			learned[gpio] = on
		} else if was != on {
			l.emit(OnOffChange{GPIO: gpio, On: on, Level: s.Level(gpio), Inferred: true})
		}
		l.known[gpio] = on
	}
	for gpio := range l.known {
		if !s.IsMonitored(gpio) {
			delete(l.known, gpio)
		}
	}
	if len(learned) > 0 {
		l.emit(InitialOnOff{On: learned})
	}
}

func (l *watchLoop) isActiveLow(gpio int) bool {
	if low, ok := l.activeLow[gpio]; ok {
		return low
	}
	return l.config[gpio].Mode == ModePullUp
}

func (l *watchLoop) onOff(s Status) map[int]bool {
	on := map[int]bool{}
	for _, gpio := range MaskToGPIOs(s.Monitored) {
		on[gpio] = s.Level(gpio) != l.isActiveLow(gpio)
	}
	return on
}

// setConfig records the pin configuration of the device and forgets the ON/OFF of pins that are
// no longer monitored or whose pull changed: their pin level is read anew (PROTOCOL.md 6.4).
func (l *watchLoop) setConfig(config PinConfig) {
	for gpio := range l.known {
		if !config[gpio].IsMonitored() || config[gpio].Mode != l.config[gpio].Mode {
			delete(l.known, gpio)
		}
	}
	l.config = config
}

// configChanged handles CONFIG_CHANGED: it follows the new configuration, or writes the target
// back when another program changed it.
func (l *watchLoop) configChanged() error {
	report, err := l.conn.device.GetPinConfig()
	if err != nil {
		l.lost(err)
		return nil
	}
	changed := report.Config != l.config
	l.setConfig(report.Config)
	if l.target == nil {
		if changed {
			l.emit(PinConfigChanged{Config: report.Config})
		}
		return nil
	}
	if report.Config == *l.target {
		return nil
	}
	if wait := restoreInterval - time.Since(l.lastRestore); wait > 0 {
		l.restore.set(wait)
		return nil
	}
	return l.restoreTarget()
}

func (l *watchLoop) restoreTarget() error {
	c := l.conn
	if c == nil || l.target == nil {
		return nil
	}
	l.restore.stop()
	l.lastRestore = time.Now()
	report, err := c.device.GetPinConfig()
	if err != nil {
		l.lost(err)
		return nil
	}
	found := report.Config
	l.setConfig(found)
	if found == *l.target {
		return nil
	}
	if _, err := c.device.SetPinConfig(*l.target); err != nil {
		var rejected *PinConfigRejectedError
		var conflict *PinConfigConflictError
		switch {
		case errors.As(err, &rejected):
			return err
		case errors.As(err, &conflict):
			l.restore.set(restoreInterval)
		default:
			l.lost(err)
		}
		return nil
	}
	l.restoreCount++
	l.emit(PinConfigRestored{Found: found, Count: l.restoreCount})
	l.wrote(found, *l.target)
	return nil
}

func (l *watchLoop) setTarget(config PinConfig) error {
	if l.info != nil {
		if result, gpio := ValidatePinConfig(config, l.info.Available); result != ResultOK {
			return &PinConfigRejectedError{Result: result, GPIO: gpio, Local: true}
		}
	}
	previous := l.target
	l.target = &config
	c := l.conn
	if c == nil {
		return nil
	}
	l.restore.stop()
	report, err := c.device.GetPinConfig()
	if err != nil {
		return err
	}
	found := report.Config
	l.setConfig(found)
	if found == config {
		return nil
	}
	if _, err := c.device.SetPinConfig(config); err != nil {
		var rejected *PinConfigRejectedError
		var conflict *PinConfigConflictError
		switch {
		case errors.As(err, &rejected):
			l.target = previous
		case errors.As(err, &conflict):
			l.restore.set(restoreInterval)
		}
		return err
	}
	l.wrote(found, config)
	return nil
}

// wrote records that config was written over found.
func (l *watchLoop) wrote(found, config PinConfig) {
	l.setConfig(config)
	l.conn.awaitingConfig = true
	l.settle.set(settleTimeout)
	if driven := drivenOutputs(found, config); len(driven) > 0 {
		l.emit(OutputsReset{GPIOs: driven, Cause: OutputResetPinConfig})
	}
}

// drivenOutputs lists the pins that writing next over prev drives to their initial output level:
// pins that become outputs and outputs whose initial output level changes (PROTOCOL.md 6.4).
func drivenOutputs(prev, next PinConfig) []int {
	var gpios []int
	for gpio := range GPIOCount {
		if next[gpio].IsOutput() && prev[gpio] != next[gpio] {
			gpios = append(gpios, gpio)
		}
	}
	return gpios
}

// deviceClock converts device timestamps (µs since the device started) to host time. A report
// reaches the host some time after the device stamped it, so the sample with the smallest
// difference between host time and device time is the most accurate. The best sample of the
// current and the previous clockWindow is used, so the estimate follows the drift between the
// two clocks.
type deviceClock struct {
	cur, prev clockSample
	curStart  time.Time
}

type clockSample struct {
	host   time.Time
	device uint64
	ok     bool
}

// closer reports whether s has a smaller host-minus-device difference than o.
func (s clockSample) closer(o clockSample) bool {
	return s.host.Sub(o.host) < time.Duration(int64(s.device)-int64(o.device))*time.Microsecond
}

func (c *deviceClock) add(host time.Time, device uint64) {
	s := clockSample{host: host, device: device, ok: true}
	if !c.cur.ok || host.Sub(c.curStart) >= clockWindow {
		c.prev, c.cur, c.curStart = c.cur, s, host
		return
	}
	if s.closer(c.cur) {
		c.cur = s
	}
}

func (c *deviceClock) at(device uint64) time.Time {
	best := c.cur
	if c.prev.ok && c.prev.closer(best) {
		best = c.prev
	}
	return best.host.Add(time.Duration(int64(device)-int64(best.device)) * time.Microsecond).Round(0)
}

// alarm is a timer that is off until it is set.
type alarm struct {
	timer *time.Timer
}

func (a *alarm) set(d time.Duration) {
	a.stop()
	a.timer = time.NewTimer(d)
}

func (a *alarm) stop() {
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
}

// C is the channel the alarm fires on; it is nil, so never ready, while the alarm is off.
func (a *alarm) C() <-chan time.Time {
	if a.timer == nil {
		return nil
	}
	return a.timer.C
}

func (a *alarm) fired() {
	a.timer = nil
}
