# hidpin

**English** | [日本語](README.ja.md)

hidpin turns an RP2040 or RP2350 board into a device that watches its GPIO pins and reports their
state to a computer over USB-HID. The board appears as a **vendor-defined HID device**, so it runs on
the drivers the operating system already ships. It does not pretend to be a keyboard or a mouse;
through the bundled host libraries, an application can read the state of every pin as it is.

Boards with a preset:

| Board                 | Available GPIOs         | Status LED                                         |
| --------------------- | ----------------------- | -------------------------------------------------- |
| Raspberry Pi Pico     | 26 (GPIO0–22, 26–28)    | on-board LED (GPIO25): on, off briefly on activity |
| Adafruit QT Py RP2040 | 13 (GPIO3–6, 20, 22–29) | NeoPixel (GPIO12): green, blue briefly on activity |
| Raspberry Pi Pico 2   | 26 (GPIO0–22, 26–28)    | on-board LED (GPIO25): on, off briefly on activity |

Any other RP2040 or RP2350A board that the Pico SDK knows can be used too, by giving its available
GPIOs when building (see [Build the firmware](#1-build-the-firmware)).
Boards with an RP2350B (48 GPIOs) are not supported for now.

The status LED lights as soon as the board is powered.

> [!WARNING]
> On the first RP2350 stepping (A2), the internal pull-downs cannot hold an input low (erratum E9).
> The board reports this: `hidpin info` shows it, and the host tools warn when a pin is monitored
> with the pull-down. Use the pull-up, or an external pull-down of 8.2 kΩ or less with `nopull`.
> The default configuration uses pull-ups and is not affected.

## What it does

- Reports the **pin level** (HIGH/LOW) of monitored pins when it changes, once per second, and
  whenever the host asks for it
- Reports **edge events** (when a change began) with microsecond timestamps, up to 7 per report
- Per-pin configuration: monitored or not, pull-up/pull-down, debounce time (0–255 ms), output
- Drives output pins, returning them to a safe value (their initial output level) when USB is
  disconnected or suspended
- Several boards at once, told apart by their USB serial number (the board's unique ID)

As hidpin takes essentially every pin usable as a GPIO, the board cannot also serve analogue inputs
(ADC), I2C or SPI bridging, or keyboard input.

## Getting started

### 1. Build the firmware

You need CMake 3.20 or newer, Ninja and `arm-none-eabi-gcc`. The Pico SDK is taken from
`PICO_SDK_PATH` when it is set, and otherwise fetched (release 2.3.1) during configuration.

```
cmake -S firmware -B build/pico -G Ninja -DPICO_BOARD=pico   # Pico 2: pico2, QT Py: adafruit_qtpy_rp2040
cmake --build build/pico
```

For a board without a preset, list its available GPIOs. Leave out the GPIOs wired to the board's own
parts; on the Pico W, for example, GPIO23–25 and 29 go to the wireless chip:

```
cmake -S firmware -B build/pico_w -G Ninja -DPICO_BOARD=pico_w -DHIDPIN_GPIOS=0-22,26-28
```

Configuring stops with an error when `HIDPIN_GPIOS` is missing for such a board, and when the board has
an RP2350B. `HIDPIN_GPIOS` also replaces the preset of the boards above. A board without a preset
reports board type 0 and its Pico SDK name (`hidpin info` shows it).

Add `-DHIDPIN_DEBUG=ON` for a build that also exposes a USB serial (CDC) interface carrying
debug logs.

The status LED is the board's own unless you choose another GPIO when configuring:

```
-DHIDPIN_STATUS_LED_PIN=15                               # a plain LED, lit when HIGH
-DHIDPIN_STATUS_LED_PIN=16 -DHIDPIN_STATUS_LED_TYPE=ws2812  # a WS2812 (NeoPixel)
```

That GPIO is then left out of the preset, so it can no longer be monitored or driven. A list given
with `HIDPIN_GPIOS` must not include the GPIOs of the status LED.

### 2. Flash the firmware

Hold BOOTSEL while plugging in the board (the BOOT button on the QT Py), then copy the `hidpin.uf2`
of the build directory (`build/pico/hidpin.uf2` above) onto the drive that appears. The drive
disappears by itself and the board restarts with hidpin.

### 3. Set up the host (Linux)

```
sudo cp udev/60-hidpin.rules /etc/udev/rules.d/
sudo udevadm control --reload-rules && sudo udevadm trigger
```

**Replug the board** afterwards; the permissions of an already-created device node do not change.

To get the command itself, use any of:

```
uv tool install ./python      # hidpin available everywhere
pipx install ./python         # the same
pip install ./python          # into the current Python environment
```

To try it without installing, run `cd python && uv run hidpin list`.

A Go version of the same command and a Go driver library live in [`hidpin/`](hidpin/README.md)
(`go install github.com/yukkeorg/hidpin/cmd/hidpin@latest`, `import "github.com/yukkeorg/hidpin/hidpin"`,
Linux only).

On Linux the library talks to `/dev/hidraw*` directly, so no extra library is needed. Windows
and macOS need hidapi (`pip install './python[hidapi]'`). `HIDPIN_BACKEND=hidraw` or
`HIDPIN_BACKEND=hidapi` picks one explicitly. The hidapi wheels on PyPI are built with the
libusb backend, which fails with `OSError: open failed` when it cannot take the interface from
the kernel driver — hence hidraw being the default on Linux.

Python 3.11 or newer. Tested on Linux (hidraw); Windows and macOS should work through hidapi,
but have not been tried.

### 4. Use the command

```
hidpin list                             # connected devices, with their serial numbers
hidpin info                             # board, firmware version, available GPIOs
hidpin watch                            # keep printing status reports
hidpin config get                       # the current pin configuration
hidpin config set 5=pullup:20 6=off     # change only the pins named
hidpin config set 7=out:low             # make it an output (initial output level LOW)
hidpin output 7=high                    # change the value of an output
```

Every command takes `--json` for machine-readable output, and `--serial` to pick one of several
connected boards.

Switches are meant to be wired to GND and used with a pull-up. Pressing one pulls the pin LOW,
so the host reads "LOW = ON" by default. Change it per pin with, for example,
`hidpin watch --active-high 7`.

### 5. Use it as a library

```python
from hidpin import Device, PinMode, PinSetting

with Device.open() as device:                      # Device.open("SERIAL") picks one of several
    device.update_pins({5: PinSetting.monitor(PinMode.PULLUP, 20)})
    print(device.info.board_name, device.info.available_gpios)

    while True:
        report = device.read_status(timeout_ms=1000)
        if report is None:
            continue
        for event in report.events:
            print(event.gpio, "HIGH" if event.level else "LOW", event.start_us(report.timestamp_us))
        print(device.on_off(report))                # {5: True, 6: False, ...}
```

## Repository layout

```
CONTEXT.md            glossary (device, monitored pin, pin level, status report, edge event…)
docs/CONCEPT.md       the original concept
docs/PROTOCOL.md      the USB-HID protocol version 1 (the authority)
docs/TESTING.md       what to check on real hardware
docs/adr/             records of design decisions
protocol/vectors.json test data both the C and the Python tests read
firmware/core/        the core, free of SDK dependencies (reports, debouncing, events, engine)
firmware/app/         the firmware built on the Pico SDK and TinyUSB
firmware/test/        unit tests for the core, run on a PC
python/               the Python library and CLI (package hidpin)
hidpin/               the Go library (package hidpin; Linux, no cgo), see hidpin/README.md
cmd/hidpin/           the Go CLI
go.mod                the Go module github.com/yukkeorg/hidpin
udev/                 the Linux udev rule
```

The documents in `docs/` are written in English, each with a Japanese translation next to it
(`*-ja.md`, for example [docs/PROTOCOL-ja.md](docs/PROTOCOL-ja.md)); the glossary `CONTEXT.md` is in
Japanese.

To change the protocol, edit `docs/PROTOCOL.md` and its Japanese translation `docs/PROTOCOL-ja.md`,
update `protocol/vectors.json`, then bring both
implementations in line. The specification is the authority and the test data follows from it.

## Development

```
# tests for the firmware core (no Pico SDK required)
cmake -S firmware/test -B build/core-tests -G Ninja
cmake --build build/core-tests && ctest --test-dir build/core-tests

# tests for the host side
cd python && uv run --group dev pytest    # Python
go test ./...                             # Go, at the repository root
```

## About the USB identifiers

hidpin has its own USB identifiers, **1209:6870**, allocated by
[pid.codes](https://pid.codes/1209/6870/). The firmware, the host tools and the udev rule use them
by default.

If you redistribute a device whose firmware you have changed, give it a PID of its own.

Building and connecting with other ids:

```
cmake -S firmware -B build/pico -G Ninja -DPICO_BOARD=pico -DHIDPIN_USB_PID=0x0001
cmake --build build/pico

HIDPIN_PID=0x0001 hidpin list        # tell the host side the same number
```

`HIDPIN_USB_VID` / `HIDPIN_USB_PID` are the firmware side, `HIDPIN_VID` / `HIDPIN_PID` the host
side. The `idProduct` in the udev rule (`udev/60-hidpin.rules`) has to be edited by hand.

A build directory keeps the PID it was configured with. One configured before the switch to
1209:6870 still builds 1209:0001: configure it again with `-DHIDPIN_USB_PID=0x6870`, or delete it.

## Licence

MIT License ([LICENSE](LICENSE)).

Third-party code included here:

- `firmware/app/ws2812.pio` — from pico-examples. Copyright (c) 2020 Raspberry Pi (Trading) Ltd.,
  BSD-3-Clause
- `firmware/pico_sdk_import.cmake` — from the Pico SDK. Copyright (c) 2020 Raspberry Pi (Trading)
  Ltd., BSD-3-Clause

The Pico SDK (BSD-3-Clause) and TinyUSB (MIT) fetched at build time, and hidapi (BSD-3-Clause /
GPL-3.0 / its own licence, at your choice) used at run time, stay under the licences of their
own distributions.
