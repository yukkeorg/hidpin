---
status: accepted
---

# Build for any Pico SDK board, with the available GPIOs given at build time

**English** | [日本語](0005-any-sdk-board-with-gpio-list-ja.md)

The firmware used to accept three boards only, each with a board id and its available GPIOs written into the source. RP2040 and RP2350 boards are many, so adding each one to the source would not keep up.
The firmware now builds for any RP2040 or RP2350A board of the Pico SDK. `PICO_BOARD` stays, because the SDK takes from it more than the pins: the flash chip and its size, the crystal start-up delay, and whether an RP2350 is the A or the B package. The chip is read from the SDK; the board code of hidpin is gone.
The available GPIOs are given as a list such as `-DHIDPIN_GPIOS=0-22,26-28` when configuring. The Pico, the Pico 2 and the QT Py RP2040 keep a preset with their board id (1–3); any other board must be given the list, sends board id 0, and reports its Pico SDK name in the device information (`sdk_board`, PROTOCOL.md 5.4), a compatible change within protocol version 1.

## Considered Options

- Default to all 30 GPIOs when no list is given: some boards wire GPIOs to their own parts, such as the power supply mode, VBUS and VSYS sensing on the Pico 2 or the wireless chip on the Pico W, and pull-ups or outputs on them could upset the board. Requiring the list makes the builder look at the schematic.
- Use GPIO0–29 of an RP2350B board: the B package has 48 GPIOs and protocol version 1 cannot describe the other 18, so the build stops instead (PROTOCOL.md 9 leaves such chips to a later version).
- Report only `board` = 0 and no name: the host could not tell the user which board it is talking to.
- Give the GPIOs as a hexadecimal mask: a list of numbers and ranges is easier to read and to check against a pinout.

## Consequences

- Configuring stops with an error for a board without a preset and no `HIDPIN_GPIOS`, for an RP2350B board, and for a list that is malformed, outside GPIO0–29 or includes the GPIO of a status LED set with `HIDPIN_STATUS_LED_PIN`. A list that includes the board's own LED, known only from its header, stops the compilation.
- The WS2812 program is generated for every build, since whether the board's LED is a WS2812 is known only from its header.
- Hosts show the Pico SDK name for a board with board id 0. Firmware older than the field sends an empty name.
