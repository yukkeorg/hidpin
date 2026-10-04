# Checking on real hardware

**English** | [日本語](TESTING-ja.md)

How to check on real hardware what the unit tests on a PC (`firmware/test`, `python/tests`, and the Go tests in `hidpin/`) cannot.
Terms follow [CONTEXT.md](../CONTEXT.md) (written in Japanese), and the report contents are in [PROTOCOL.md](./PROTOCOL.md).

## What you need

- A Raspberry Pi Pico or an Adafruit QT Py RP2040
- A USB cable that carries data (a charge-only cable is not recognised)
- One tactile switch and jumper wires (connected between GPIO5 and GND)
- One LED and a resistor of about 330 Ω (to check outputs: GPIO7 → resistor → LED → GND)
- On the host

  ```
  sudo cp udev/60-hidpin.rules /etc/udev/rules.d/
  sudo udevadm control --reload-rules && sudo udevadm trigger
  uv tool install ./python      # to try without installing: cd python && uv run hidpin ...
  ```

  Replug the board after installing the rule. While it stays plugged in, its permissions do not change and
  `OSError: open failed` persists. On Linux hidraw is used directly, so hidapi does not need to be
  installed.

The wiring is 3.3 V. **The RP2040 is not 5 V tolerant**, so do not connect signals from other equipment directly.

## 1. Flashing

1. Build the firmware.

   ```
   cmake -S firmware -B build/pico -G Ninja -DPICO_BOARD=pico   # adafruit_qtpy_rp2040 for the QT Py
   cmake --build build/pico
   ```
2. Hold the BOOTSEL button (the BOOT button on the QT Py) while plugging in USB.
3. A USB drive named `RPI-RP2` appears; copy `build/pico/hidpin.uf2` onto it.

- [ ] After the copy, the drive disappears by itself and the board restarts

## 2. Recognised on USB

```
lsusb | grep 1209:6870
hidpin list
hidpin info
```

- [ ] `lsusb` shows `1209:6870`
- [ ] `hidpin list` shows a serial number (16 hexadecimal digits)
- [ ] The board name and the available GPIOs in `hidpin info` match the board (26 for the Pico, 13 for the QT Py)
- [ ] The status LED lights as soon as the power is on (the on-board LED on the Pico, the NeoPixel in green on the QT Py)
- [ ] `dmesg` shows it as `hidraw`, not as a keyboard or a mouse

Unplugging the USB cable turns the LED off (the power goes too).

## 3. Status reports and switch input

Connect the switch between GPIO5 and GND. The default pin configuration uses pull-ups, so pressing it gives LOW.

```
hidpin watch
```

- [ ] The first line shows ON/OFF for every monitored pin, and GPIO5 is `OFF` (HIGH, as it is not pressed)
- [ ] Pressing the switch prints one line, `GPIO5  ON  (LOW)`
- [ ] Releasing it prints one line, `GPIO5  OFF (HIGH)`
- [ ] One press and release prints no extra lines (chattering is removed)
- [ ] The status LED changes briefly on press and on release (blue on the QT Py, off on the Pico)
- [ ] The difference between the times shown roughly matches how long the switch was held

`hidpin --json watch` prints one JSON report per line. `start_us` in `events` is the time the change started.

- [ ] Even while the switch is held, the periodic report every second (`PERIODIC` in `reason`) keeps coming

## 4. Effect of the debounce time

```
hidpin config set 5=pullup:0     # no debouncing
hidpin watch
```

- [ ] A single press can print several lines (the contact bounce shows as it is)

```
hidpin config set 5=pullup:20    # back to the default
```

- [ ] Each press and each release is back to one line

## 5. Reading and writing the pin configuration

```
hidpin config get
hidpin config set 6=off
hidpin config get
hidpin config set 23=pullup:20   # not an available GPIO on the Pico
```

- [ ] After `config set 6=off`, GPIO6 disappears from the `config get` list
- [ ] `watch` shows `the pin configuration changed` (`CONFIG_CHANGED` in `reason`)
- [ ] Naming a pin that is not an available GPIO fails, and the configuration does not change
- [ ] Unplugging and replugging USB returns the pin configuration to the default (every pin `pullup:20`)

## 6. Outputs

Connect the LED to GPIO7.

```
hidpin config set 7=out:low
hidpin output 7=high
hidpin output 7=low
```

- [ ] The LED is off once the pin is set to `out:low`
- [ ] `output 7=high` turns the LED on
- [ ] The status LED changes briefly on every output command (blue on the QT Py, off on the Pico)
- [ ] `output 7=low` turns it off
- [ ] `hidpin watch` shows the change of the output (`OUTPUT_APPLIED` in `reason`)
- [ ] Naming a GPIO that is not an output pin prints a warning and changes nothing

Checking USB suspend (put the PC to sleep, or use `/sys/bus/usb/devices/.../power/control`):

- [ ] Suspending with the LED on **returns it to the initial output level (LOW), so it goes off**
- [ ] After resuming, `watch` shows `outputs returned to their initial level …` (`OUTPUT_RESET` in `reason`)

`OUTPUT_RESET` is set on the first status report right after reconnection. If `watch` was stopped before
reconnecting, it is not received (that the outputs are back at their initial level can be checked in `levels`).
A USB bus reset behaves the same. To try one without root, send `USBDEVFS_RESET`
(`_IO('U', 20)` = `0x5514`) with ioctl to the USB node behind the hidraw device.

## 7. Detecting missed reports and dropped events

```
hidpin --json watch > /tmp/hidpin.jsonl
```

Operate the switch several times, then stop and check the record.

- [ ] `seq` increases by 1 each time (`missed_reports` stays 0)
- [ ] `OVERFLOW` does not appear in `flags`

Cause many changes in a short time, for example by rubbing the switch terminals together.

- [ ] Even if `OVERFLOW` appears in `flags`, the `levels` that follow match the actual state of the pins

## 8. Two boards at once (if you have two)

- [ ] `hidpin list` shows two serial numbers
- [ ] Running `hidpin info` without `--serial` asks which one to use
- [ ] With `--serial`, only that device is operated

## 9. Debug build

```
cmake -S firmware -B build/pico-debug -G Ninja -DPICO_BOARD=pico -DHIDPIN_DEBUG=ON
cmake --build build/pico-debug
```

After flashing:

- [ ] `hidpin list` still works (the HID side looks the same)
- [ ] `/dev/ttyACM0` appears, and logs are printed on operations such as `hidpin config set`

  ```
  screen /dev/ttyACM0 115200     # or: cat /dev/ttyACM0
  ```

## 10. Other operating systems (optional)

Windows and macOS are not "supported", but this is what to check when running hidpin on them.

- [ ] The board is recognised without installing an extra driver
- [ ] `hidpin list` and `hidpin watch` work
