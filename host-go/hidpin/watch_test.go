package hidpin_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/yukkeorg/hidpin/host-go/hidpin"
	"github.com/yukkeorg/hidpin/host-go/hidpin/hidpintest"
)

const (
	serialA = "ABCD0123456789EF"
	serialB = "FEDC9876543210AB"
)

func startWatch(t *testing.T, opts hidpin.WatchOptions) *hidpin.Watcher {
	t.Helper()
	if opts.ScanInterval == 0 {
		opts.ScanInterval = 5 * time.Millisecond
	}
	w, err := hidpin.Watch(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

func pins(t *testing.T, settings map[int]hidpin.PinSetting) *hidpin.PinConfig {
	t.Helper()
	config, err := hidpin.PinConfig{}.WithPins(settings)
	if err != nil {
		t.Fatal(err)
	}
	return &config
}

func next(t *testing.T, w *hidpin.Watcher) hidpin.Event {
	t.Helper()
	select {
	case event, ok := <-w.Events():
		if !ok {
			t.Fatalf("the watcher stopped: %v", w.Err())
		}
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("no event arrived")
	}
	return nil
}

// await skips events until one of type T arrives.
func await[T hidpin.Event](t *testing.T, w *hidpin.Watcher) T {
	t.Helper()
	for {
		if event, ok := next(t, w).(T); ok {
			return event
		}
	}
}

// expectNo fails if an event of type T arrives within d.
func expectNo[T hidpin.Event](t *testing.T, w *hidpin.Watcher, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case event, ok := <-w.Events():
			if !ok {
				t.Fatalf("the watcher stopped: %v", w.Err())
			}
			if _, bad := event.(T); bad {
				t.Fatalf("unexpected %#v", event)
			}
		case <-deadline:
			return
		}
	}
}

func awaitStop(t *testing.T, w *hidpin.Watcher) error {
	t.Helper()
	for {
		select {
		case _, ok := <-w.Events():
			if !ok {
				return w.Err()
			}
		case <-time.After(3 * time.Second):
			t.Fatal("the watcher did not stop")
		}
	}
}

// ownerWatch starts a watcher with a target pin configuration on a plugged-in board and waits
// until it knows the ON/OFF of the monitored pins: GPIO5 OFF (pull-up, HIGH) and GPIO6 ON
// (pull-down, HIGH).
func ownerWatch(t *testing.T) (*hidpin.Watcher, *hidpintest.Bus, *hidpintest.Board, *hidpin.PinConfig) {
	t.Helper()
	board := hidpintest.NewBoard(serialA)
	bus := hidpintest.NewBus(board)
	target := pins(t, map[int]hidpin.PinSetting{5: hidpin.MonitorPullUp(20), 6: hidpin.MonitorPullDown(0), 7: hidpin.Output(false)})
	w := startWatch(t, hidpin.WatchOptions{TargetPins: target, Bus: bus})
	initial := await[hidpin.InitialOnOff](t, w)
	if len(initial.On) != 2 || initial.On[5] || !initial.On[6] {
		t.Fatalf("initial ON/OFF %v", initial.On)
	}
	return w, bus, board, target
}

func TestWatchWritesTheTargetAndLearnsOnOff(t *testing.T) {
	board := hidpintest.NewBoard(serialA)
	bus := hidpintest.NewBus(board)
	target := pins(t, map[int]hidpin.PinSetting{5: hidpin.MonitorPullUp(20), 6: hidpin.MonitorPullDown(0), 7: hidpin.Output(false)})
	w := startWatch(t, hidpin.WatchOptions{TargetPins: target, Bus: bus})

	connected := await[hidpin.Connected](t, w)
	if connected.Serial != serialA || connected.Reconnected || connected.PinConfig != *target || connected.Info.Board != hidpin.BoardPico {
		t.Errorf("connected %+v", connected)
	}
	if board.PinConfig() != *target {
		t.Errorf("the board has %v", board.PinConfig())
	}
	reset := next(t, w).(hidpin.OutputsReset)
	if !slices.Equal(reset.GPIOs, []int{7}) || reset.Cause != hidpin.OutputResetPinConfig {
		t.Errorf("outputs reset %+v", reset)
	}
	status := await[hidpin.StatusReceived](t, w)
	if status.Status.Reason&hidpin.ReasonConfigChanged == 0 || status.Status.Monitored != 1<<5|1<<6 {
		t.Errorf("status %+v", status.Status)
	}
	initial := next(t, w).(hidpin.InitialOnOff)
	if len(initial.On) != 2 || initial.On[5] || !initial.On[6] {
		t.Errorf("initial ON/OFF %v", initial.On)
	}
}

func TestWatchReportsTimedChanges(t *testing.T) {
	w, _, board, _ := ownerWatch(t)
	board.SetInput(5, false)
	status := await[hidpin.StatusReceived](t, w)
	change := next(t, w).(hidpin.OnOffChange)
	if change.GPIO != 5 || !change.On || change.Level || change.Inferred || !change.HasTime {
		t.Fatalf("change %+v", change)
	}
	if change.DeviceUS != status.Status.TimestampUS-20_000 {
		t.Errorf("began at %d us, report at %d us", change.DeviceUS, status.Status.TimestampUS)
	}
	if age := time.Since(change.Time); age < 0 || age > 5*time.Second {
		t.Errorf("began %v ago", age)
	}
	if !status.On[5] || !status.On[6] {
		t.Errorf("status ON/OFF %v", status.On)
	}
}

func TestWatchReconnectsAfterUnplugging(t *testing.T) {
	w, bus, board, target := ownerWatch(t)
	board.SetInput(5, false)
	if change := await[hidpin.OnOffChange](t, w); !change.On {
		t.Fatalf("change %+v", change)
	}

	bus.Unplug(board)
	if disconnected := await[hidpin.Disconnected](t, w); !errors.Is(disconnected.Err, hidpintest.ErrGone) {
		t.Errorf("disconnected %v", disconnected.Err)
	}
	if err := w.SetOutputs(map[int]bool{7: true}); !errors.Is(err, hidpin.ErrDisconnected) {
		t.Errorf("SetOutputs while disconnected: %v", err)
	}
	board.SetInput(5, true)  // GPIO5 back to OFF
	board.SetInput(6, false) // GPIO6 to OFF
	bus.Plug(board)          // the board starts again with the default pin configuration

	connected := await[hidpin.Connected](t, w)
	if !connected.Reconnected || board.PinConfig() != *target {
		t.Errorf("connected %+v, board has %v", connected, board.PinConfig())
	}
	if reset := next(t, w).(hidpin.OutputsReset); reset.Cause != hidpin.OutputResetReconnect || !slices.Equal(reset.GPIOs, []int{7}) {
		t.Errorf("outputs reset %+v", reset)
	}
	changes := map[int]hidpin.OnOffChange{}
	for len(changes) < 2 {
		switch event := next(t, w).(type) {
		case hidpin.OnOffChange:
			changes[event.GPIO] = event
		case hidpin.InitialOnOff:
			t.Fatalf("pins known before were learned again: %v", event.On)
		}
	}
	for _, gpio := range []int{5, 6} {
		if change := changes[gpio]; change.On || !change.Inferred || change.HasTime {
			t.Errorf("GPIO%d: %+v", gpio, change)
		}
	}
}

func TestWatchStaysWithTheFirstBoard(t *testing.T) {
	a, b := hidpintest.NewBoard(serialA), hidpintest.NewBoard(serialB)
	bus := hidpintest.NewBus(a)
	w := startWatch(t, hidpin.WatchOptions{Bus: bus})
	if connected := await[hidpin.Connected](t, w); connected.Serial != serialA {
		t.Fatalf("connected to %s", connected.Serial)
	}
	bus.Unplug(a)
	await[hidpin.Disconnected](t, w)
	bus.Plug(b)
	expectNo[hidpin.Connected](t, w, 100*time.Millisecond)
	if b.OpenCount() != 0 {
		t.Error("the other board was opened")
	}
	bus.Plug(a)
	if connected := await[hidpin.Connected](t, w); connected.Serial != serialA || !connected.Reconnected {
		t.Errorf("connected %+v", connected)
	}
}

func TestWatchStopsWhenItCannotChooseABoard(t *testing.T) {
	bus := hidpintest.NewBus(hidpintest.NewBoard(serialA), hidpintest.NewBoard(serialB))
	w := startWatch(t, hidpin.WatchOptions{Bus: bus})
	if err := awaitStop(t, w); !errors.Is(err, hidpin.ErrSeveralDevices) {
		t.Errorf("stopped with %v", err)
	}
	if err := w.SetOutputs(map[int]bool{7: true}); !errors.Is(err, hidpin.ErrWatcherClosed) {
		t.Errorf("SetOutputs after stopping: %v", err)
	}
}

func TestWatchStopsWhenTheTargetDoesNotSuitTheBoard(t *testing.T) {
	bus := hidpintest.NewBus(hidpintest.NewBoard(serialA))
	w := startWatch(t, hidpin.WatchOptions{TargetPins: pins(t, map[int]hidpin.PinSetting{23: hidpin.MonitorPullUp(20)}), Bus: bus})
	var rejected *hidpin.PinConfigRejectedError
	if err := awaitStop(t, w); !errors.As(err, &rejected) || rejected.GPIO != 23 {
		t.Errorf("stopped with %v", err)
	}
}

func TestWatchRejectsBadOptions(t *testing.T) {
	bus := hidpintest.NewBus()
	bad := hidpin.PinConfig{}
	bad[3] = hidpin.PinSetting{Mode: hidpin.ModeUnused, Param: 1}
	for name, opts := range map[string]hidpin.WatchOptions{
		"target":   {TargetPins: &bad, Bus: bus},
		"polarity": {ActiveLow: map[int]bool{30: true}, Bus: bus},
		"interval": {ScanInterval: -time.Second, Bus: bus},
	} {
		if _, err := hidpin.Watch(context.Background(), opts); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestWatchWritesTheTargetBack(t *testing.T) {
	w, _, board, target := ownerWatch(t)
	other := *target
	other[5], other[7] = hidpin.Unused(), hidpin.Unused()
	if result := board.WritePinConfig(other); result != hidpin.ResultOK {
		t.Fatal(result)
	}
	restored := await[hidpin.PinConfigRestored](t, w)
	if restored.Found != other || restored.Count != 1 || board.PinConfig() != *target {
		t.Errorf("restored %+v, board has %v", restored, board.PinConfig())
	}
	if reset := next(t, w).(hidpin.OutputsReset); reset.Cause != hidpin.OutputResetPinConfig || !slices.Equal(reset.GPIOs, []int{7}) {
		t.Errorf("outputs reset %+v", reset)
	}
	// GPIO5 was not monitored for a moment, so its ON/OFF is learned again.
	if initial := await[hidpin.InitialOnOff](t, w); len(initial.On) != 1 || initial.On[5] {
		t.Errorf("initial ON/OFF %v", initial.On)
	}
}

func TestWatchWithoutTargetFollowsTheDevice(t *testing.T) {
	board := hidpintest.NewBoard(serialA)
	w := startWatch(t, hidpin.WatchOptions{Bus: hidpintest.NewBus(board)})
	defaults := hidpin.DefaultPinConfig(hidpintest.PicoAvailable)
	if connected := await[hidpin.Connected](t, w); connected.PinConfig != defaults {
		t.Errorf("connected %+v", connected)
	}
	if initial := await[hidpin.InitialOnOff](t, w); len(initial.On) != 26 || initial.On[5] {
		t.Errorf("initial ON/OFF %v", initial.On)
	}
	other := defaults
	other[5] = hidpin.Unused()
	board.WritePinConfig(other)
	if changed := await[hidpin.PinConfigChanged](t, w); changed.Config != other {
		t.Errorf("changed to %v", changed.Config)
	}
	expectNo[hidpin.PinConfigRestored](t, w, 50*time.Millisecond)
	if board.PinConfig() != other {
		t.Error("a watcher without a target wrote the pin configuration")
	}
}

func TestWatchInfersLostChanges(t *testing.T) {
	w, _, board, _ := ownerWatch(t)

	board.SkipReports(2)
	board.Periodic()
	if status := await[hidpin.StatusReceived](t, w); status.Missed != 2 {
		t.Errorf("missed %d", status.Missed)
	}

	// The device dropped the edge event: only the level tells.
	board.LoseInput(5, false)
	board.Periodic()
	if change := await[hidpin.OnOffChange](t, w); change.GPIO != 5 || !change.On || !change.Inferred || change.HasTime {
		t.Errorf("change %+v", change)
	}

	// GPIO5 went OFF without an edge event and then ON with one: the OFF is inferred from the
	// edge event, which must have left the other level.
	board.LoseInput(5, true)
	board.SetInput(5, false)
	first, second := await[hidpin.OnOffChange](t, w), next(t, w).(hidpin.OnOffChange)
	if first.On || !first.Inferred || !second.On || second.Inferred || !second.HasTime {
		t.Errorf("changes %+v then %+v", first, second)
	}
}

func TestWatchPolarityOverride(t *testing.T) {
	board := hidpintest.NewBoard(serialA)
	target := pins(t, map[int]hidpin.PinSetting{5: hidpin.MonitorPullUp(20)})
	w := startWatch(t, hidpin.WatchOptions{TargetPins: target, ActiveLow: map[int]bool{5: false}, Bus: hidpintest.NewBus(board)})
	if initial := await[hidpin.InitialOnOff](t, w); !initial.On[5] {
		t.Errorf("GPIO5 HIGH should be ON: %v", initial.On)
	}
}

func TestWatchReportsOutputResets(t *testing.T) {
	w, _, board, target := ownerWatch(t)
	if err := w.SetOutputs(map[int]bool{7: true}); err != nil || !board.Level(7) {
		t.Fatalf("SetOutputs: %v, level %v", err, board.Level(7))
	}

	board.Suspend()
	if reset := await[hidpin.OutputsReset](t, w); reset.Cause != hidpin.OutputResetSuspend || !slices.Equal(reset.GPIOs, []int{7}) {
		t.Errorf("outputs reset %+v", reset)
	}
	if board.Level(7) {
		t.Error("the output was driven back")
	}

	board.Reenumerate()
	await[hidpin.Disconnected](t, w)
	if connected := await[hidpin.Connected](t, w); !connected.Reconnected || connected.PinConfig != *target {
		t.Errorf("connected %+v", connected)
	}
	if reset := next(t, w).(hidpin.OutputsReset); reset.Cause != hidpin.OutputResetReconnect {
		t.Errorf("outputs reset %+v", reset)
	}
}

func TestSetTargetPins(t *testing.T) {
	w, bus, board, target := ownerWatch(t)

	changed := *target
	changed[8] = hidpin.Output(true)
	if err := w.SetTargetPins(changed); err != nil || board.PinConfig() != changed {
		t.Fatalf("SetTargetPins: %v, board has %v", err, board.PinConfig())
	}
	if reset := await[hidpin.OutputsReset](t, w); !slices.Equal(reset.GPIOs, []int{8}) || !board.Level(8) {
		t.Errorf("outputs reset %+v", reset)
	}

	unsuitable := changed
	unsuitable[23] = hidpin.MonitorPullUp(20)
	var rejected *hidpin.PinConfigRejectedError
	if err := w.SetTargetPins(unsuitable); !errors.As(err, &rejected) || board.PinConfig() != changed {
		t.Errorf("unsuitable target: %v", err)
	}

	bus.Unplug(board)
	await[hidpin.Disconnected](t, w)
	later := *target
	later[5] = hidpin.MonitorPullDown(5)
	if err := w.SetTargetPins(later); err != nil {
		t.Fatal(err)
	}
	if err := w.SetTargetPins(unsuitable); !errors.As(err, &rejected) {
		t.Errorf("unsuitable target while disconnected: %v", err)
	}
	bus.Plug(board)
	if connected := await[hidpin.Connected](t, w); connected.PinConfig != later || board.PinConfig() != later {
		t.Errorf("connected with %v", connected.PinConfig)
	}
}

func TestWatchReportsConnectFailuresOnce(t *testing.T) {
	board := hidpintest.NewBoard(serialA)
	board.SetOpenError(os.ErrPermission)
	w := startWatch(t, hidpin.WatchOptions{Bus: hidpintest.NewBus(board)})
	if failed := await[hidpin.ConnectFailed](t, w); !errors.Is(failed.Err, os.ErrPermission) {
		t.Errorf("failed with %v", failed.Err)
	}
	expectNo[hidpin.ConnectFailed](t, w, 50*time.Millisecond)

	board.SetOpenError(nil)
	board.SetProtocolVersion(2)
	var version *hidpin.ProtocolVersionError
	if failed := await[hidpin.ConnectFailed](t, w); !errors.As(failed.Err, &version) {
		t.Errorf("failed with %v", failed.Err)
	}
	board.SetProtocolVersion(hidpin.ProtocolVersion)
	await[hidpin.Connected](t, w)
}

func TestWatchOnlyReportsFailuresThatLast(t *testing.T) {
	board := hidpintest.NewBoard(serialA)
	board.SetOpenError(os.ErrPermission)
	w := startWatch(t, hidpin.WatchOptions{Bus: hidpintest.NewBus(board), ScanInterval: 200 * time.Millisecond})
	time.Sleep(50 * time.Millisecond) // one failed try: udev may not have set the permissions yet
	board.SetOpenError(nil)
	if event := next(t, w); !isConnected(event) {
		t.Errorf("first event %#v", event)
	}
}

func isConnected(event hidpin.Event) bool {
	_, ok := event.(hidpin.Connected)
	return ok
}

func TestMethodsCanBeCalledWhileEventsWait(t *testing.T) {
	board := hidpintest.NewBoard(serialA)
	target := pins(t, map[int]hidpin.PinSetting{7: hidpin.Output(false)})
	w := startWatch(t, hidpin.WatchOptions{TargetPins: target, Bus: hidpintest.NewBus(board)})
	await[hidpin.Connected](t, w)
	// More events are waiting to be received; the watcher must still serve this call.
	done := make(chan error, 1)
	go func() { done <- w.SetOutputs(map[int]bool{7: true}) }()
	select {
	case err := <-done:
		if err != nil || !board.Level(7) {
			t.Errorf("SetOutputs: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SetOutputs blocked while events were waiting")
	}
}

func TestCloseWithoutReceiving(t *testing.T) {
	board := hidpintest.NewBoard(serialA)
	w := startWatch(t, hidpin.WatchOptions{Bus: hidpintest.NewBus(board)})
	for board.OpenCount() == 0 {
		time.Sleep(time.Millisecond)
	}
	for i := range 10 {
		board.SetInput(5, i%2 == 1)
	}
	closed := make(chan struct{})
	go func() {
		w.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close blocked")
	}
	if err := awaitStop(t, w); err != nil || board.OpenCount() != 0 {
		t.Errorf("stopped with %v, %d transports open", err, board.OpenCount())
	}
	if err := w.SetTargetPins(hidpin.PinConfig{}); !errors.Is(err, hidpin.ErrWatcherClosed) {
		t.Errorf("SetTargetPins after Close: %v", err)
	}
}

func TestCancellingTheContextStopsTheWatcher(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w, err := hidpin.Watch(ctx, hidpin.WatchOptions{Bus: hidpintest.NewBus(hidpintest.NewBoard(serialA))})
	if err != nil {
		t.Fatal(err)
	}
	await[hidpin.Connected](t, w)
	cancel()
	if err := awaitStop(t, w); err != nil {
		t.Errorf("stopped with %v", err)
	}
}
