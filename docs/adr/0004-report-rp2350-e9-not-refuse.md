---
status: accepted
---

# Report RP2350 erratum E9 and warn on the host, instead of refusing pull-downs

**English** | [日本語](0004-report-rp2350-e9-not-refuse-ja.md)

On the first RP2350 stepping (A2), an input pad whose voltage falls between the logic levels leaks current and stays at about 2.2 V, and the internal pull-down is too weak to pull it low (erratum RP2350-E9, fixed in the A4 stepping).
A pin monitored with the pull-down (`mode` = 3) can therefore keep reading HIGH when it is left open.
The stepping cannot be told from the board type; only the device can read it, through `rp2350_chip_version()` of the Pico SDK.
So the device reports the chip, its revision and a `PULL_DOWN_UNRELIABLE` flag in the reserved bytes of the device information (PROTOCOL.md 5.2, 5.3), which is a compatible change within protocol version 1.
The device still accepts `mode` = 3, and the host warns: the Python library with `warnings.warn`, and both CLIs on stderr.
The Go library prints nothing, as Go libraries usually do not, and leaves the warning to the caller through `DeviceInfo.PullDownUnreliable()`.

## Considered Options

- Only document the problem: the setting would fail silently, and the user would see a pin that never goes LOW without knowing why.
- Refuse `mode` = 3 on an A2 chip: an input driven by a low-impedance source (8.2 kΩ or less, or a push-pull output) works even on A2, so refusing would break valid wiring, and the same host program would succeed on one board and fail on another depending on the stepping.

## Consequences

- Firmware from before these fields sends 0, so the host cannot tell and does not warn. Firmware from this version on always reports the chip.
- The warning fires for every write whose resulting configuration still has pull-down pins, not only for the pins just changed, because the risk lasts as long as the setting does.
