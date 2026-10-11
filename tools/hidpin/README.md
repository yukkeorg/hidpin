# The hidpin command

The official command-line tool for hidpin devices, written in Go on the host library in
[`hidpin/`](../../hidpin/README.md). Linux only: it talks to `/dev/hidraw*` directly, with no cgo
and no other dependency.

## Install

```
go install github.com/yukkeorg/hidpin/tools/hidpin@latest
```

or, from the root of a checkout (plain `go build ./tools/hidpin` names the binary `hidpin`, which
clashes with the `hidpin/` directory):

```
go install ./tools/hidpin            # into $GOPATH/bin
go build -o bin/hidpin ./tools/hidpin
```

Non-root access needs the udev rule (`sudo cp udev/60-hidpin.rules /etc/udev/rules.d/`, then
replug the board). A firmware built with other USB ids is found with `HIDPIN_VID` / `HIDPIN_PID`.

## Use

```
hidpin list                                     # connected devices, with their serial numbers
hidpin info                                     # board, firmware version, chip, available GPIOs
hidpin watch [--active-low 5,6] [--active-high 7] [--all]
hidpin config get
hidpin config set 5=pullup:20 6=off 7=out:low   # change only the pins named
hidpin output 7=high
```

`--json` prints machine-readable output and `--serial` picks one of several boards; options may come
anywhere on the line. `watch` keeps going when the board is unplugged and plugged in again (it still
fails at once when no board is connected). JSON is printed without spaces between items; with
`--json`, messages about the connection go to stderr, so that stdout stays a stream of status reports.

A pin setting is one of `off`, `nopull:N`, `pullup:N`, `pulldown:N` (N: debounce time 0-255 ms,
20 when left out), `out:low` and `out:high`. On an RP2350 with erratum E9, `info` reports it and
`config set` warns when a pin uses the internal pull-down.

## Develop

```
go test ./tools/...                  # the tests run the commands against a fake device and a simulated bus
go vet ./...
```

The tests need no hardware: `hidpin/hidpintest` stands in for the board.
