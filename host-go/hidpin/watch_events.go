package hidpin

import (
	"fmt"
	"time"
)

// Event is one notification from a Watcher. It is one of Connected, Disconnected, ConnectFailed,
// StatusReceived, InitialOnOff, OnOffChange, OutputsReset, PinConfigChanged and
// PinConfigRestored.
type Event interface {
	watchEvent()
}

// Connected means the watcher opened the device and, when it has a target pin configuration,
// made the device follow it.
type Connected struct {
	Serial string
	Path   string
	Info   DeviceInfo
	// PinConfig is the pin configuration of the device from now on.
	PinConfig PinConfig
	// Reconnected is false for the first connection and true for every later one.
	Reconnected bool
}

// Disconnected means the connection was lost, usually because the device was unplugged. The
// watcher keeps looking for the same device.
type Disconnected struct {
	Err error
}

// ConnectFailed means the device is connected but could not be used, for example because of
// missing permissions or another protocol version. The watcher keeps trying. The same cause is
// reported once, not on every try.
type ConnectFailed struct {
	Err error
}

// StatusReceived carries a status report as the device sent it, including the ones the watcher
// requests itself (ReasonHostRequest) right after connecting.
type StatusReceived struct {
	Status Status
	// Missed counts the status reports the host failed to read just before this one.
	Missed int
	// On is ON/OFF of every monitored pin in this report, interpreted with the polarity.
	On map[int]bool
}

// InitialOnOff gives the ON/OFF of monitored pins the host did not know before: every monitored
// pin after the first connection, and pins that became monitored or changed their pull later.
// It is not an ON/OFF change.
type InitialOnOff struct {
	On map[int]bool
}

// OnOffChange means the ON/OFF of a monitored pin changed.
type OnOffChange struct {
	GPIO int
	On   bool
	// Level is the pin level after the change (HIGH = true).
	Level bool
	// HasTime reports whether the moment the change began is known. When it is, Time is that
	// moment estimated in host time (accurate to a few milliseconds) and DeviceUS is the same
	// moment on the device clock, in microseconds since the device started; they are zero when
	// it is not.
	HasTime  bool
	Time     time.Time
	DeviceUS uint64
	// Inferred is true when no edge event reported the change: the watcher found it by comparing
	// pin levels after edge events were lost or the device was disconnected. Such a change has no
	// time.
	Inferred bool
}

// OutputResetCause tells why output pins went back to their initial output level.
type OutputResetCause uint8

const (
	// OutputResetSuspend: the device reported a USB suspend or bus reset (OUTPUT_RESET).
	OutputResetSuspend OutputResetCause = iota + 1
	// OutputResetReconnect: the device was connected again after it had been disconnected.
	OutputResetReconnect
	// OutputResetPinConfig: a pin configuration write made the pins outputs or changed their
	// initial output level.
	OutputResetPinConfig
)

func (c OutputResetCause) String() string {
	switch c {
	case OutputResetSuspend:
		return "USB suspend or bus reset"
	case OutputResetReconnect:
		return "reconnected"
	case OutputResetPinConfig:
		return "pin configuration written"
	}
	return fmt.Sprintf("cause %d", uint8(c))
}

// OutputsReset means output pins are at their initial output level now. The watcher never drives
// them back to the levels it was told earlier; the application decides whether to.
type OutputsReset struct {
	GPIOs []int
	Cause OutputResetCause
}

// PinConfigChanged means the pin configuration of the device changed. Only a watcher without a
// target pin configuration reports it.
type PinConfigChanged struct {
	Config PinConfig
}

// PinConfigRestored means another program changed the pin configuration and the watcher wrote
// its target pin configuration back. Found is what it found on the device. Count is how many
// times the watcher has done this; a count that keeps growing means another program keeps
// changing it.
type PinConfigRestored struct {
	Found PinConfig
	Count int
}

func (Connected) watchEvent()         {}
func (Disconnected) watchEvent()      {}
func (ConnectFailed) watchEvent()     {}
func (StatusReceived) watchEvent()    {}
func (InitialOnOff) watchEvent()      {}
func (OnOffChange) watchEvent()       {}
func (OutputsReset) watchEvent()      {}
func (PinConfigChanged) watchEvent()  {}
func (PinConfigRestored) watchEvent() {}
