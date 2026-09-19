# hidpin for Go

A Go driver library and CLI for hidpin devices, the Go counterpart of the Python code in
[`../host`](../host). It speaks the protocol in [docs/PROTOCOL.md](../docs/PROTOCOL.md) and is
tested against the same [`protocol/vectors.json`](../protocol/vectors.json).

- **Linux only.** It talks to `/dev/hidraw*` directly: pure Go, no cgo, no hidapi, no other
  dependency. On other systems `FindDevices` and `Open` return `ErrUnsupportedPlatform`.
- Module `github.com/yukkeorg/hidpin/host-go`, Go 1.22 or newer.

| Package | Contents |
|---|---|
| `hidpin` | the driver library: reports, device access, pin configuration, polarity |
| `hidpin/hidpintest` | a fake device, to test code that uses the library without hardware |
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
Python CLI, options may come anywhere on the line. JSON is printed without spaces between items.

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

## Tests

```
cd host-go
go test ./...
```
