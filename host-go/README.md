# hidpin for Go

A Go driver library and CLI for hidpin devices, the Go counterpart of the Python code in
[`../host`](../host). It speaks the protocol in [docs/PROTOCOL.md](../docs/PROTOCOL.md) and is
tested against the same [`protocol/vectors.json`](../protocol/vectors.json).

- **Linux only.** It talks to `/dev/hidraw*` directly: pure Go, no cgo, no hidapi, no other
  dependency. On other systems `FindDevices` and `Open` return `ErrUnsupportedPlatform`.
- Module `github.com/yukkeorg/hidpin/host-go`, Go 1.22 or newer.

| Package | Contents |
|---|---|
| `hidpin` | the driver library: reports, device access, pin configuration, polarity, and `Watch` for long-running programs |
| `hidpin/hidpintest` | a fake device and a simulated bus of boards, to test code that uses the library without hardware |
| `cmd/hidpin` | the `hidpin` command |
| `internal/hidraw` | the Linux hidraw backend (sysfs enumeration, HIDIOC* ioctls) |

## The command

```
go install github.com/yukkeorg/hidpin/host-go/cmd/hidpin@latest
```

or, from a checkout (plain `go build ./cmd/hidpin` clashes with the `hidpin/` directory):

```
cd host-go
go install ./cmd/hidpin            # into $GOPATH/bin
go build -o bin/hidpin ./cmd/hidpin
```

It takes the same commands and options as the Python CLI:

```
hidpin list
hidpin info
hidpin watch [--active-low 5,6] [--active-high 7] [--all]
hidpin config get
hidpin config set 5=pullup:20 6=off 7=out:low
hidpin output 7=high
```

`--json` prints machine-readable output and `--serial` picks one of several boards. Unlike the
Python CLI, options may come anywhere on the line, and `watch` keeps going when the board is
unplugged and plugged in again (it still fails at once when no board is connected). JSON is printed
without spaces between items; with `--json`, messages about the connection go to stderr.

Non-root access needs the udev rule (`sudo cp udev/70-hidpin.rules /etc/udev/rules.d/`, then
replug the board). A firmware built with other USB ids is found with `HIDPIN_VID` / `HIDPIN_PID`.

## The library

```go
import "github.com/yukkeorg/hidpin/host-go/hidpin"

device, err := hidpin.Open("") // the only connected board, or pass its serial number
if err != nil {
	log.Fatal(err)
}
defer device.Close()

if _, err := device.UpdatePins(map[int]hidpin.PinSetting{
	5: hidpin.MonitorPullUp(20), // a switch to GND, debounced for 20 ms
	7: hidpin.Output(false),     // an output that starts (and falls back to) LOW
}); err != nil {
	log.Fatal(err)
}
config, _ := device.PinConfig()

device.SetOutputs(map[int]bool{7: true})

for {
	status, ok, err := device.ReadStatus(time.Second)
	if err != nil {
		log.Fatal(err)
	}
	if !ok {
		continue
	}
	for _, event := range status.Events {
		start, _ := event.StartUS(status.TimestampUS)
		fmt.Println(event.GPIO, event.Level, start)
	}
	fmt.Println(device.OnOff(status, config)) // map[5:true ...]
}
```

`SetPinConfig` and `UpdatePins` verify every write as PROTOCOL.md 6.3 describes and return
`*PinConfigRejectedError` (with `Local` set when the library caught it before sending) or
`*PinConfigConflictError` when another process wrote at the same time. `MissedReports` counts
status reports the host failed to read.

To test your own code without a board, wrap `hidpintest.New(hidpin.ProtocolVersion)` with
`hidpin.NewDevice`; see the package example.

### Long-running programs

`Device` stops working when the board is unplugged. `Watch` keeps one board connected instead: it
waits for the board, opens it again after it comes back, keeps the pin configuration at a target
and reports what happens on a channel. Its methods are safe to call from any goroutine, including
the one receiving the events.

```go
target, _ := hidpin.PinConfig{}.WithPins(map[int]hidpin.PinSetting{
	5: hidpin.MonitorPullUp(20), // pins not listed are unused
	7: hidpin.Output(false),
})
w, err := hidpin.Watch(ctx, hidpin.WatchOptions{Serial: "E6614864D3417F2A", TargetPins: &target})
if err != nil {
	log.Fatal(err)
}
defer w.Close()

for event := range w.Events() {
	switch e := event.(type) {
	case hidpin.InitialOnOff: // pins whose ON/OFF was not known, e.g. after starting
		fmt.Println(e.On)
	case hidpin.OnOffChange: // e.Time is zero when e.HasTime is false
		fmt.Println(e.GPIO, e.On, e.Time)
	case hidpin.OutputsReset: // the outputs are at their initial level again
		w.SetOutputs(map[int]bool{7: true}) // only if that is safe now
	case hidpin.Disconnected, hidpin.ConnectFailed: // the watcher keeps trying
		log.Println(e)
	}
}
if err := w.Err(); err != nil { // nil after Close or a cancelled ctx
	log.Fatal(err)
}
```

- **One board.** Without `Serial`, the watcher takes the first board it finds and from then on
  waits for that one only; a different board plugged in later is left alone.
- **The target pin configuration** is written after every connection (a board that lost power
  starts with every pin monitored) and again when another program changes it, reported as
  `PinConfigRestored`. Without `TargetPins`, the watcher never writes the pin configuration and
  reports changes as `PinConfigChanged`; `hidpin watch` works that way.
- **Outputs are not driven back.** After a USB suspend or reconnection, or when a write of the pin
  configuration drives them, the output pins are at their initial output level and the watcher
  reports `OutputsReset`; whether to drive them again is up to the program
  ([ADR 0002](../docs/adr/0002-restore-pin-config-not-outputs.md)). `SetOutputs` fails with
  `ErrDisconnected` while the board is away.
- **ON/OFF changes** come from edge events and carry the time the change began, in host time
  (`Time`, accurate to a few milliseconds) and on the device clock (`DeviceUS`). Changes whose edge
  events were lost, or that happened while the board was away, are found by comparing pin levels;
  they have `Inferred` set and no time. `InitialOnOff` is not a change.
- **Errors.** Problems that may go away (permissions, another protocol version) are reported as
  `ConnectFailed` while the watcher keeps trying. It stops only when the target does not suit the
  board (`*PinConfigRejectedError`) or when it cannot choose a board (`ErrSeveralDevices`).
- `StatusReceived` carries every status report as the device sent it, with `Missed` counting the
  reports the host failed to read.

To test a program that uses `Watch`, pass `hidpintest.NewBus(hidpintest.NewBoard(serial))` as
`Bus`; the board can be unplugged, plugged in, suspended and have its inputs changed.

## Tests

```
cd host-go
go test ./...
```
