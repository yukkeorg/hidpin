---
status: accepted
---

# Name the project `hidpin`, free of trademarks

**English** | [日本語](0001-neutral-project-name-ja.md)

The working name from the concept stage, "RPI204x-IO", puts Raspberry Pi's trademark at the start of the name in a way that could be mistaken for an official product, and "204x" is a series that does not exist under the RP2040 part-numbering scheme.
Public names (the repository, the PyPI package, the CLI command and the pid.codes registration) are hard to change once they spread, so we adopt `hidpin`, which contains neither a trademark nor a chip name, and show the supported boards in descriptions as a statement of compatibility, as in "for RP2040 boards (Raspberry Pi Pico / Adafruit QT Py RP2040)".

## Considered Options

- `rpi204x-io` (the concept name as it is): it has two problems, the risk of trademark confusion and a part-number series that does not exist.
- `rp2040-io` (including the chip name): we could not confirm from primary sources whether the chip name is a trademark, and the name would no longer match what the project is once support extends to the RP2350 (Pico 2).
