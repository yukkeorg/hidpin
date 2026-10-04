# Draft of the pid.codes application

**English** | [日本語](README-ja.md)

A draft for registering a PID of this project's own under the USB VID `0x1209` (pid.codes).
For the conditions and the procedure, see the [pid.codes how-to](https://pid.codes/howto/).

> **Status**: accepted on 2026-09-28 ([PR #1280](https://github.com/pidcodes/pidcodes.github.com/pull/1280)).
> The registration is at <https://pid.codes/1209/6870/>. The defaults were switched to `1209:6870` on 2026-10-04.

## What is submitted

| File | Location in the forked repository |
|---|---|
| `org/yukke.org/index.md` | Organisation page |
| `1209/6870/index.md` | Device page (PID = `0x6870`, "hp" in ASCII) |

As of 2026-09-17, `0x6870` collided with neither the 921 registered PIDs nor the 85 open PRs.
Before applying, check the [list under 1209](https://github.com/pidcodes/pidcodes.github.com/tree/master/1209)
and the open PRs again.

## Prerequisites (met)

pid.codes requires "publicly available source" and "an open source licence".
This repository is public at <https://github.com/yukkeorg/hidpin> and has an MIT `LICENSE`.
The `site` and `source` in the draft point to this URL.

If you fork this project to get a PID of your own, change `owner`, `site` and `source` to yours.

## How to apply

1. Fork [pidcodes/pidcodes.github.com](https://github.com/pidcodes/pidcodes.github.com)
2. Copy the contents of `org/` and `1209/` in this directory to the same places in the fork's working tree
3. Commit and send a pull request (one PID per device; asking for more needs an explanation)

## What was done after acceptance (2026-10-04)

The defaults were switched from `1209:0001` (the test PID) to the allocated `1209:6870`. These places were changed:

- the default of `HIDPIN_USB_PID` in `firmware/CMakeLists.txt`, and `HIDPIN_USB_PID` in `firmware/app/usb_descriptors.c`
- `DEFAULT_PRODUCT_ID` in `host/src/hidpin/device.py`, and its tests
- `DefaultProductID` in `host-go/hidpin/device.go`, and its tests
- `idProduct` in `udev/60-hidpin.rules` (two lines)
- the text of `docs/PROTOCOL.md` §1, `docs/TESTING.md`, `README.md` and `README.ja.md`
- the default in `firmware-rs/app/build.rs` and `firmware-rs/README.md` on the `rust-firmware` branch

After switching, flash the firmware again, reinstall the udev rule and replug the board.

A CMake build directory remembers in its cache the `HIDPIN_USB_PID` it was configured with.
A directory configured before the switch still builds `0x0001`, so configure it again.

```
cmake -S firmware -B build/pico -DHIDPIN_USB_PID=0x6870
cmake --build build/pico        # then flash again
```
