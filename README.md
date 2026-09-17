# hidpin

**English** | [日本語](README.ja.md)

hidpin watches the GPIO pins of an RP2040 board and reports their state to a computer over
USB-HID. The board appears as a **vendor-defined HID device**, so it runs on the drivers the
operating system already ships. It does not pretend to be a keyboard or a mouse, which is why
an application on the host can read the state of every pin as it is.

Supported boards:

| Board | Available GPIOs | Status LED |
|---|---|---|
| Raspberry Pi Pico | 26 (GPIO0–22, 26–28) | on-board LED (GPIO25) |
| Adafruit QT Py RP2040 | 13 (GPIO3–6, 20, 22–29) | NeoPixel (GPIO12) |

> **Status**: the tests on a PC pass (32 for the firmware core, 56 for the host), and the
> software has been **verified on a real Adafruit QT Py RP2040** (2026-09-17): enumeration,
> monitoring and edge events, debouncing, pin configuration, outputs, and the recovery after a
> bus reset. The Raspberry Pi Pico has not been tried yet. The procedure is in
> [docs/TESTING.md](docs/TESTING.md).

Most documents in this repository are written in Japanese; the protocol specification
([docs/PROTOCOL.md](docs/PROTOCOL.md)) is the authority on the wire format.

## What it does

- Reports the **pin level** (HIGH/LOW) of monitored pins when it changes, once per second, and
  whenever the host asks for it
- Reports **edge events** (when a change began) with microsecond timestamps, up to 7 per report
- Per-pin configuration: monitored or not, pull-up/pull-down, debounce time (0–255 ms), output
- Drives output pins, returning them to a safe value (their initial output level) when USB is
  disconnected or suspended
- Several boards at once, told apart by their USB serial number (the board's unique ID)

Out of scope: analogue inputs (ADC), I2C or SPI bridging, acting as a keyboard.

## Getting started

### 1. Flash the firmware

You need CMake 3.20 or newer, Ninja and `arm-none-eabi-gcc`. The Pico SDK is taken from
`PICO_SDK_PATH` when it is set, and otherwise fetched (release 2.3.1) during configuration.

```
cmake -S firmware -B build/pico -G Ninja -DPICO_BOARD=pico   # QT Py: adafruit_qtpy_rp2040
cmake --build build/pico
```

Hold BOOTSEL while plugging in the board, then copy `build/pico/hidpin.uf2` onto the drive that
appears.

Add `-DHIDPIN_DEBUG=ON` for a build that also exposes a USB serial (CDC) interface carrying
debug logs.

### 2. Set up the host (Linux)

```
sudo cp udev/70-hidpin.rules /etc/udev/rules.d/
sudo udevadm control --reload-rules && sudo udevadm trigger
```

**Replug the board** afterwards; the permissions of an already-created device node do not change.

To get the command itself, use any of:

```
uv tool install ./host      # hidpin available everywhere
pipx install ./host         # the same
pip install ./host          # into the current Python environment
```

To try it without installing, run `cd host && uv run hidpin list`.

On Linux the library talks to `/dev/hidraw*` directly, so no extra library is needed. Windows
and macOS need hidapi (`pip install './host[hidapi]'`). `HIDPIN_BACKEND=hidraw` or
`HIDPIN_BACKEND=hidapi` picks one explicitly. The hidapi wheels on PyPI are built with the
libusb backend, which fails with `OSError: open failed` when it cannot take the interface from
the kernel driver — hence hidraw being the default on Linux.

Python 3.11 or newer. Tested on Linux (hidraw); Windows and macOS should work through hidapi,
but have not been tried.

### 3. Use the command

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

### 4. Use it as a library

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
docs/pid-codes/       the pid.codes registration for the USB product ID
protocol/vectors.json test data both the C and the Python tests read
firmware/core/        the core, free of SDK dependencies (reports, debouncing, events, engine)
firmware/app/         the firmware built on the Pico SDK and TinyUSB
firmware/test/        unit tests for the core, run on a PC
host/                 the Python library and CLI
udev/                 the Linux udev rule
```

To change the protocol, edit `docs/PROTOCOL.md`, update `protocol/vectors.json`, then bring both
implementations in line. The specification is the authority and the test data follows from it.

## Development

```
# tests for the firmware core (no Pico SDK required)
cmake -S firmware/test -B build/core-tests -G Ninja
cmake --build build/core-tests && ctest --test-dir build/core-tests

# tests for the host side
cd host && uv run --group dev pytest
```

## About the USB identifiers

The default is the pid.codes test PID **1209:0001**, which is **for in-house testing only** and
must not be used on a device that is redistributed, sold or manufactured. Get your own PID from
[pid.codes](https://pid.codes/howto/) — free if the source is public under an open source
licence.

Building and connecting with an allocated number:

```
cmake -S firmware -B build/pico -G Ninja -DPICO_BOARD=pico -DHIDPIN_USB_PID=0x1234
cmake --build build/pico

HIDPIN_PID=0x1234 hidpin list        # tell the host side the same number
```

`HIDPIN_USB_VID` / `HIDPIN_USB_PID` are the firmware side, `HIDPIN_VID` / `HIDPIN_PID` the host
side. The `idProduct` in the udev rule (`udev/70-hidpin.rules`) has to be edited by hand.

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
