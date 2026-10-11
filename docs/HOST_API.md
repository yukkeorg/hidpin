# hidpin host library API (version 1)

**English** | [日本語](HOST_API-ja.md)

This document specifies the API that every **host library** of hidpin provides, whatever its language.
It is the specification to follow when a host library is written in a new language, and the reference
the existing ones are checked against: Python ([`python/`](../python)), Go ([`hidpin/`](../hidpin)) and Rust
([`rust/`](../rust)).

- The wire format is defined by [PROTOCOL.md](PROTOCOL.md), which is the authority on bytes and on the
  behaviour of the **device**. This document defines what a host library offers on top of it.
- The terms in bold are those of the glossary [CONTEXT.md](../CONTEXT.md).
- The key words MUST, MUST NOT, SHOULD, SHOULD NOT and MAY are to be read as in RFC 2119 and RFC 8174
  when they appear in capitals.

## 1. Scope and conformance

The API has two layers.

| Layer | Contents | Requirement | Python | Go | Rust |
|---|---|---|---|---|---|
| **Core** | Constants, types, encoding and decoding of every report, finding and opening devices, the `Device` operations (sections 3–8) | A host library MUST provide all of it | Yes | Yes | Yes |
| **Watch** | A long-running watcher that waits for the device, reopens it, keeps the pin configuration at a target and reports events (section 9) | MAY be provided; when provided, it MUST behave as section 9 says | Yes | Yes | Yes |

A host library MAY add functions of its own (the command-line tools are outside this document), but it
MUST NOT change the meaning of the operations named here.

A host library MUST pass every case in [`protocol/vectors.json`](../protocol/vectors.json) (section 10).

## 2. Naming

Each operation and type has a **canonical name**, written here in `snake_case` for functions and fields
and `PascalCase` for types. A host library spells it in the idiom of its language (for example
`read_status` in Python and `ReadStatus` in Go) and SHOULD keep the words, so that code reads the same in
every language. The tables in this document give the names the existing libraries use.

- An operation that may fail returns or raises an error in the usual way of the language: exceptions in
  Python, an `error` result in Go.
- Where a language has no optional arguments, an argument this document marks as optional becomes a
  required one or a separate function (Go: `Device.OnOff(status, config)`).
- Bitmasks of GPIOs are unsigned 32-bit integers, bit *n* standing for GPIO*n* (PROTOCOL.md 2).
- GPIO numbers are plain integers from 0 to 29.
- Times on the device are microseconds since it powered on, as unsigned 64-bit integers.

The Rust library uses the canonical names as they are, so the tables leave it out except for errors (8)
and the Watch layer (9). Its few departures: `ResultCode` for `Result` (so as not to hide Rust's own),
`PinConfig::default_for(available)` for `default(available)` (beside the `Default` trait),
`PinSetting::pull_up` / `pull_down` / `no_pull` beside `monitor`, GPIO numbers as `u8`, and "none" as
`Option`. Errors are the variants of one enum, `hidpin::Error`. Its examples are in
[rust/README.md](../rust/README.md) and the crate documentation (`cargo doc`).

Most sections end with usage examples in Python and Go. Where an example needs an open device, it is
called `device` (section 7.1). The Go examples are the body of a function that returns `error`, and
the Watch examples also have a `context.Context` called `ctx`. A comment after a statement shows what
it prints or what it does.

## 3. Constants

| Canonical name | Value | Python | Go |
|---|---|---|---|
| `PROTOCOL_VERSION` | 1 | `PROTOCOL_VERSION` | `ProtocolVersion` |
| `REPORT_ID_STATUS` | `0x01` | `protocol.REPORT_ID_STATUS` | `ReportIDStatus` |
| `REPORT_ID_DEVICE_INFO` | `0x02` | `protocol.REPORT_ID_DEVICE_INFO` | `ReportIDDeviceInfo` |
| `REPORT_ID_PIN_CONFIG` | `0x03` | `protocol.REPORT_ID_PIN_CONFIG` | `ReportIDPinConfig` |
| `REPORT_ID_OUTPUT` | `0x04` | `protocol.REPORT_ID_OUTPUT` | `ReportIDOutput` |
| `REPORT_PAYLOAD_LEN` | 63 | `protocol.REPORT_PAYLOAD_LEN` | `PayloadLen` |
| `OUTPUT_PAYLOAD_LEN` | 8 | `protocol.OUTPUT_PAYLOAD_LEN` | `OutputPayloadLen` |
| `GPIO_COUNT` | 30 | `protocol.GPIO_COUNT` | `GPIOCount` |
| `EVENTS_PER_REPORT` | 7 | `protocol.EVENTS_PER_REPORT` | `EventsPerReport` |
| `DEFAULT_DEBOUNCE_MS` | 20 | `protocol.DEFAULT_DEBOUNCE_MS` | `DefaultDebounceMS` |
| `RESULT_GPIO_NONE` | `0xFF` | `protocol.RESULT_GPIO_NONE` | `ResultGPIONone` |
| `SATURATED_AGE_US` | `0xFFFFFFFF` | `protocol.SATURATED_AGE_US` | `SaturatedAgeUS` |
| `SDK_BOARD_LEN` | 40 | `protocol.SDK_BOARD_LEN` | `SDKBoardLen` |
| `DEFAULT_VENDOR_ID` | `0x1209` | `device.DEFAULT_VENDOR_ID` | `DefaultVendorID` |
| `DEFAULT_PRODUCT_ID` | `0x6870` | `device.DEFAULT_PRODUCT_ID` | `DefaultProductID` |

Usage:

**Python**

```python
from hidpin import protocol

print(protocol.GPIO_COUNT, protocol.DEFAULT_DEBOUNCE_MS)  # 30 20
```

**Go**

```go
fmt.Println(hidpin.GPIOCount, hidpin.DefaultDebounceMS) // 30 20
```

## 4. Types

Every type below is a value: decoding a report gives a new value, and nothing changes it afterwards.
A language with immutable records SHOULD use them.

### 4.1 Enumerations and flags

Each enumeration keeps unknown values instead of failing on them (PROTOCOL.md 9: a newer device may send
values this library does not know).

| Canonical name | Kind | Values | Python | Go |
|---|---|---|---|---|
| `Reason` | flags | `LEVEL_CHANGED` 0x01, `OUTPUT_APPLIED` 0x02, `CONFIG_CHANGED` 0x04, `PERIODIC` 0x08, `OUTPUT_RESET` 0x10, `HOST_REQUEST` 0x80 | `Reason` (`IntFlag`) | `Reason`, `Reason*` |
| `StatusFlags` | flags | `OVERFLOW` 0x01, `MORE_EVENTS` 0x02 | `StatusFlags` | `StatusFlags`, `Flag*` |
| `PinMode` | enum | `UNUSED` 0, `INPUT` 1 (no pull), `PULLUP` 2, `PULLDOWN` 3, `OUTPUT` 4 | `PinMode` | `PinMode`, `Mode*` |
| `Result` | enum | `OK` 0, `BAD_LENGTH` 1, `BAD_MODE` 2, `UNAVAILABLE_GPIO` 3, `UNUSED_WITH_PARAM` 4, `BAD_OUTPUT_LEVEL` 5 | `Result` | `Result`, `Result*` |
| `Board` | enum | `OTHER` 0, `PICO` 1, `QTPY_RP2040` 2, `PICO2` 3 | `Board` | `Board`, `Board*` |
| `Chip` | enum | 0 = not reported, `RP2040` 1, `RP2350A` 2 | `Chip` | `Chip`, `Chip*` |
| `Quirks` | flags | `PULL_DOWN_UNRELIABLE` 0x01 | `Quirk` | `Quirks`, `QuirkPullDownUnreliable` |

Required helpers:

- `PinMode.is_input`: true for `INPUT`, `PULLUP` and `PULLDOWN`.
- `result_message(result)` (Python `protocol.result_message`, Go `Result.Message()`): one line of English for each `Result`, as in this table. An unknown value gives
  `result N`.

  | `Result` | Message |
  |---|---|
  | `OK` | `applied` |
  | `BAD_LENGTH` | `the payload is not 63 bytes long` |
  | `BAD_MODE` | `mode is outside 0-4` |
  | `UNAVAILABLE_GPIO` | `the pin is not an available GPIO` |
  | `UNUSED_WITH_PARAM` | `an unused pin was given a parameter` |
  | `BAD_OUTPUT_LEVEL` | `the initial output level is neither 0 nor 1` |

- `board_name(board, sdk_board)`: the product name for `PICO` (`Raspberry Pi Pico`), `QTPY_RP2040`
  (`Adafruit QT Py RP2040`) and `PICO2` (`Raspberry Pi Pico 2`). For any other value it is `sdk_board` when
  that is not empty, otherwise `other board` for 0 and `unknown board N` for the rest.
- `chip_name(chip)`: `not reported` for 0, `RP2040`, `RP2350A`, otherwise `unknown chip N`.

Usage:

**Python**

```python
from hidpin import PinMode, Reason, Result
from hidpin.protocol import board_name, chip_name, result_message

reason = Reason.LEVEL_CHANGED | Reason.PERIODIC
print(bool(reason & Reason.PERIODIC))  # True
print(PinMode.PULLDOWN.is_input)  # True
print(result_message(Result.UNAVAILABLE_GPIO))  # the pin is not an available GPIO
print(board_name(0, "pico_w"), chip_name(2))  # pico_w RP2350A
```

**Go**

```go
reason := hidpin.ReasonLevelChanged | hidpin.ReasonPeriodic
fmt.Println(reason&hidpin.ReasonPeriodic != 0)      // true
fmt.Println(hidpin.ModePullDown.IsInput())          // true
fmt.Println(hidpin.ResultUnavailableGPIO.Message()) // the pin is not an available GPIO
info := hidpin.DeviceInfo{Board: hidpin.BoardOther, SDKBoard: "pico_w", Chip: hidpin.ChipRP2350A}
fmt.Println(info.BoardName(), info.Chip.Name()) // pico_w RP2350A
```

### 4.2 `EdgeEvent` (PROTOCOL.md 4.4)

| Field / operation | Meaning |
|---|---|
| `gpio` | GPIO number (bits 0–4 of the `pin` byte) |
| `level` | The **pin level** after the change (true = HIGH) |
| `age_us` | Microseconds from the change to `timestamp_us` of the report |
| `saturated` | True when `age_us` = `SATURATED_AGE_US` |
| `start_us(timestamp_us)` | `timestamp_us - age_us`, or "unknown" when saturated (Python `None`, Go `ok` = false) |

Usage:

**Python**

```python
from hidpin import EdgeEvent

event = EdgeEvent(gpio=5, level=False, age_us=20_000)
print(event.start_us(4_000_000))  # 3980000: 20 ms before the report
print(EdgeEvent(5, False, 0xFFFFFFFF).start_us(4_000_000))  # None: too old to tell
```

**Go**

```go
event := hidpin.EdgeEvent{GPIO: 5, Level: false, AgeUS: 20_000}
start, ok := event.StartUS(4_000_000)
fmt.Println(start, ok) // 3980000 true: 20 ms before the report
```

### 4.3 `StatusReport` (PROTOCOL.md 4)

Python `StatusReport`, Go `Status`.

| Field / operation | Meaning |
|---|---|
| `seq`, `reason`, `flags`, `monitored`, `outputs`, `levels`, `timestamp_us` | As in PROTOCOL.md 4 |
| `events` | The **edge events**, at most 7, in order |
| `overflow`, `more_events` | Whether the flag is set |
| `level(gpio)`, `is_monitored(gpio)`, `is_output(gpio)` | The bit of `levels`, `monitored`, `outputs` |

Usage:

**Python**

```python
report = device.request_status()
print(report.seq, report.reason, report.timestamp_us)
print(report.is_monitored(5), report.level(5))  # True True: monitored, HIGH
if report.overflow:
    print("the device dropped edge events")
```

**Go**

```go
status, err := device.RequestStatus()
if err != nil {
	return err
}
fmt.Println(status.Seq, status.Reason, status.TimestampUS)
fmt.Println(status.IsMonitored(5), status.Level(5)) // true true: monitored, HIGH
if status.Overflow() {
	fmt.Println("the device dropped edge events")
}
```

### 4.4 `DeviceInfo` (PROTOCOL.md 5)

| Field / operation | Meaning |
|---|---|
| `protocol_version`, `firmware` (major, minor, patch), `board`, `gpio_count`, `periodic_interval_ms`, `available`, `events_per_report`, `event_queue_size`, `chip`, `chip_revision`, `quirks`, `sdk_board` | As in PROTOCOL.md 5. `chip`, `chip_revision`, `quirks` and `sdk_board` default to 0 or empty, as older firmware sends |
| `firmware_version` | `"major.minor.patch"` |
| `available_gpios` | The GPIO numbers in `available`, ascending |
| `board_name` | `board_name(board, sdk_board)` |
| `chip_name` | `chip_name(chip)` (Go: `Chip.Name()`) |
| `pull_down_unreliable` | Whether `quirks` has `PULL_DOWN_UNRELIABLE` |

`sdk_board` is decoded as ASCII up to the first 0 byte; each byte outside ASCII becomes U+FFFD.

Usage:

**Python**

```python
info = device.info
print(info.firmware_version, info.board_name, info.chip_name)
print(info.available_gpios)  # [0, 1, ..., 22, 26, 27, 28]
if info.pull_down_unreliable:
    print("use the pull-up or an external pull-down")
```

**Go**

```go
info, err := device.Info()
if err != nil {
	return err
}
fmt.Println(info.FirmwareVersion(), info.BoardName(), info.Chip.Name())
fmt.Println(info.AvailableGPIOs()) // [0 1 ... 22 26 27 28]
if info.PullDownUnreliable() {
	fmt.Println("use the pull-up or an external pull-down")
}
```

### 4.5 `PinSetting` and `PinConfig` (PROTOCOL.md 6)

`PinSetting` is how one GPIO is used: `mode` (`PinMode`) and `param` (0–255).

| Constructor | Result | Python | Go |
|---|---|---|---|
| `unused()` | `UNUSED`, 0 | `PinSetting.unused()` | `Unused()` |
| `monitor(mode, debounce_ms)` | an input mode with its **debounce time**; a non-input mode is an error | `PinSetting.monitor(mode, debounce_ms)` | `MonitorNoPull`, `MonitorPullUp`, `MonitorPullDown` |
| `output(initial_high)` | `OUTPUT`, 1 or 0 | `PinSetting.output(level)` | `Output(initialHigh)` |

`PinSetting` also offers `is_monitored` and `is_output`, and a text form used by the CLIs: `off`,
`nopull:N`, `pullup:N`, `pulldown:N`, `out:low`, `out:high`.

`PinConfig` holds exactly `GPIO_COUNT` settings, indexed by GPIO number.

| Operation | Meaning |
|---|---|
| `unused()` | Every GPIO `UNUSED`, the start of a **target pin configuration** (Python `PinConfig.unused()`, Go the zero value `PinConfig{}`) |
| `default(available)` | `PULLUP` with `DEFAULT_DEBOUNCE_MS` for each available GPIO, `UNUSED` for the rest (PROTOCOL.md 6.1) |
| `config[gpio]` | The setting of one GPIO |
| `with_pins({gpio: setting})` | A copy with some pins replaced; a GPIO outside 0–29 is an error |
| `monitored_mask`, `outputs_mask` | Bitmasks of the monitored and output pins |
| `pull_down_gpios` | GPIOs whose mode is `PULLDOWN`, ascending |

Two `PinConfig` values are equal when every setting is equal.

Usage:

**Python**

```python
from hidpin import PinConfig, PinMode, PinSetting

config = PinConfig.unused().with_pins(
    {
        5: PinSetting.monitor(PinMode.PULLUP, 20),  # a switch to GND
        6: PinSetting.monitor(PinMode.INPUT, 0),  # no pull, no debouncing
        7: PinSetting.output(False),  # starts (and returns to) LOW
    }
)
print(config[5], config[7])  # pullup:20 out:low
print(hex(config.monitored_mask), hex(config.outputs_mask))  # 0x60 0x80
print(PinConfig.default(0x1C7FFFFF)[0])  # pullup:20, as the device starts
```

**Go**

```go
config, err := hidpin.PinConfig{}.WithPins(map[int]hidpin.PinSetting{
	5: hidpin.MonitorPullUp(20), // a switch to GND
	6: hidpin.MonitorNoPull(0),  // no pull, no debouncing
	7: hidpin.Output(false),     // starts (and returns to) LOW
})
if err != nil {
	return err
}
fmt.Println(config[5], config[7])                                     // pullup:20 out:low
fmt.Printf("%#x %#x\n", config.MonitoredMask(), config.OutputsMask()) // 0x60 0x80
fmt.Println(hidpin.DefaultPinConfig(0x1C7FFFFF)[0])                   // pullup:20, as the device starts
```

### 4.6 `PinConfigReport`

`result` (`Result`), `result_gpio`, `request_id` and `config` (`PinConfig`), as in PROTOCOL.md 6.

Usage:

**Python**

```python
report = device.get_pin_config()
print(report.result, report.result_gpio, report.request_id)  # the last write
print(report.config[5])
```

**Go**

```go
report, err := device.GetPinConfig()
if err != nil {
	return err
}
fmt.Println(report.Result, report.ResultGPIO, report.RequestID) // the last write
fmt.Println(report.Config[5])
```

## 5. Encoding and validation

These functions do no I/O. Payloads exclude the report ID byte, as in PROTOCOL.md.

| Canonical name | Python (`hidpin.protocol`) | Go |
|---|---|---|
| `decode_status(payload)` / `encode_status(report)` | `decode_status`, `encode_status` | `DecodeStatus`, `Status.Encode` |
| `decode_device_info(payload)` / `encode_device_info(info)` | `decode_device_info`, `encode_device_info` | `DecodeDeviceInfo`, `DeviceInfo.Encode` |
| `decode_pin_config(payload)` / `encode_pin_config(report)` | `decode_pin_config`, `encode_pin_config` | `DecodePinConfig`, `PinConfigReport.Encode` |
| `encode_pin_config_set(config, request_id)` | `encode_pin_config_set` | `EncodePinConfigSet` |
| `encode_output(mask, value)` / `decode_output(payload)` | `encode_output`, `decode_output` | `EncodeOutput`, `DecodeOutput` |
| `validate_pin_config(config, available)` | `validate_pin_config` | `ValidatePinConfig` |
| `validate_pin_config_payload(payload, available)` | `validate_pin_config_payload` | `ValidatePinConfigPayload` |
| `mask_to_gpios(mask)` | `mask_to_gpios` | `MaskToGPIOs` |

- A decoder MUST fail on a payload of the wrong length, on `event_count` above 7, and on a `mode` above 4.
- `encode_pin_config_set` writes `result` = 0 and `result_gpio` = `0xFF`, and fails when `request_id` is
  outside 1–255.
- `validate_pin_config_payload` repeats the checks of the device in the same order (PROTOCOL.md 6.3) and
  returns the result, the GPIO it refers to (`RESULT_GPIO_NONE` when none) and the `request_id`.
  `validate_pin_config` does the same for a `PinConfig`.
- Encoders are needed by tests and fake devices as much as by the library itself, so they MUST be public.

Usage:

**Python**

```python
from hidpin import protocol

print(protocol.encode_output(1 << 7, 1 << 7).hex())  # 8000000080000000: GPIO7 HIGH
report = protocol.decode_status(bytes(63))  # a payload without the report ID
result, gpio = protocol.validate_pin_config(config, 0x1C7FFFFF)
print(result, gpio)  # 0 255: OK, no GPIO
print(protocol.mask_to_gpios(0x60))  # [5, 6]
```

**Go**

```go
fmt.Printf("%x\n", hidpin.EncodeOutput(1<<7, 1<<7)) // 8000000080000000: GPIO7 HIGH
status, err := hidpin.DecodeStatus(make([]byte, hidpin.PayloadLen)) // a payload without the report ID
if err != nil {
	return err
}
result, gpio := hidpin.ValidatePinConfig(config, 0x1C7FFFFF)
fmt.Println(status.Seq, result, gpio) // 0 0 255: OK, no GPIO
fmt.Println(hidpin.MaskToGPIOs(0x60)) // [5 6]
```

## 6. Finding devices

| Canonical name | Meaning | Python | Go |
|---|---|---|---|
| `usb_ids()` | The vendor and product ID to look for | `device.usb_ids()` | `USBIDs()` |
| `DeviceEntry` | One device found: `path`, `serial`, `manufacturer`, `product` | `DeviceEntry` | `Entry` |
| `find_devices()` | Lists the connected hidpin devices | `find_devices()` | `FindDevices()` |
| `choose_device(entries, serial)` | Picks one entry (below) | `choose_device()` | `ChooseDevice()` |

- `usb_ids` MUST read the environment variables `HIDPIN_VID` and `HIDPIN_PID`, which override the
  defaults. A value is a number in decimal or with a `0x` prefix; anything else, or a value above
  `0xFFFF`, is an error. An empty or unset variable means the default.
- `find_devices` lists the HID interfaces with these IDs whose top-level collection is Usage Page
  `0xFF00`, Usage `0x01` (PROTOCOL.md 1). When the backend cannot tell the usage (for example when the
  report descriptor cannot be read), it relies on the IDs alone, and opening checks the protocol version
  instead. On Linux the report descriptor is in `/sys/class/hidraw/hidrawN/device/report_descriptor`;
  the Usage Page and Usage are those in force at its first Collection (Application). Entries SHOULD be
  ordered by serial number, then by path.
- `choose_device` takes the entry whose serial number equals `serial`. With no `serial`, it takes the only
  entry. It fails with "not found" when nothing matches, and with "several devices" (listing their serial
  numbers) when no `serial` was given and there are several.
- A library SHOULD choose its backend for itself (Linux: `/dev/hidraw*`, elsewhere: hidapi) and MAY let
  the user choose (Python: `HIDPIN_BACKEND` = `hidraw` / `hidapi`).

Usage:

**Python**

```python
from hidpin import choose_device, find_devices

entries = find_devices()  # HIDPIN_VID / HIDPIN_PID override the USB ids
for entry in entries:
    print(entry.serial, entry.path)
entry = choose_device(entries)  # the only one; choose_device(entries, "SERIAL") picks one
```

**Go**

```go
entries, err := hidpin.FindDevices() // HIDPIN_VID / HIDPIN_PID override the USB ids
if err != nil {
	return err
}
for _, e := range entries {
	fmt.Println(e.Serial, e.Path)
}
entry, err := hidpin.ChooseDevice(entries, "") // the only one; pass a serial number to pick one
if err != nil {
	return err
}
fmt.Println(entry.Serial)
```

## 7. `Device`

A `Device` is one open device. It need not be safe for use from several threads at once; a library MUST
say whether it is.

### 7.1 Opening and closing

| Canonical name | Meaning | Python | Go |
|---|---|---|---|
| `open(serial?)` | Finds the devices, chooses one with `choose_device` and opens it | `Device.open(serial)` | `Open(serial)` |
| `open_path(path, serial?)` | Opens a known device node | `Device.open(path=...)` | `OpenPath(path, serial)` |
| `close()` | Closes it | `close()`, `with` | `Close()` |
| `serial` | The serial number it was opened by | `serial` | `Serial` |

- Opening MUST read the device information and fail with `ProtocolVersionError` when `protocol_version`
  is not 1, before any other report is sent, and close what it opened when it fails.
- When opening fails for lack of permission, the error SHOULD say how to install the udev rule.
- A library SHOULD offer a way to build a `Device` on a transport of the caller's (a fake device in tests):
  Python takes any object with the hidapi methods, Go takes a `Transport`.

Usage:

**Python**

```python
from hidpin import Device

with Device.open() as device:  # the only connected board
    print(device.serial)
device = Device.open("ABCD0123456789EF")  # one of several, by serial number
device.close()
device = Device.open(path=b"/dev/hidraw0")  # a known device node
device.close()
```

**Go**

```go
device, err := hidpin.Open("") // the only connected board; Open("ABCD0123456789EF") picks one
if err != nil {
	return err
}
defer device.Close()
fmt.Println(device.Serial)
other, err := hidpin.OpenPath("/dev/hidraw0", "") // a known device node
if err != nil {
	return err
}
other.Close()
```

### 7.2 Reading information and configuration

| Canonical name | Meaning | Python | Go |
|---|---|---|---|
| `info` | The device information, read once and then cached | `info` (property) | `Info()` |
| `get_pin_config()` | Reads the pin configuration report now, and caches its `config` | `get_pin_config()` | `GetPinConfig()` |
| `pin_config` | The cached `config`, read only when there is none yet | `pin_config` (property) | `PinConfig()` |

Usage:

**Python**

```python
print(device.info.board_name)  # read once, then cached
report = device.get_pin_config()  # read now
config = device.pin_config  # the cached configuration
print(config[5])
```

**Go**

```go
info, err := device.Info() // read once, then cached
if err != nil {
	return err
}
if _, err := device.GetPinConfig(); err != nil { // read now
	return err
}
config, err := device.PinConfig() // the cached configuration
if err != nil {
	return err
}
fmt.Println(info.BoardName(), config[5])
```

### 7.3 Writing the pin configuration

`set_pin_config(config)` writes a whole pin configuration and confirms it, in this order:

1. Validate `config` against `info.available` with `validate_pin_config`. On failure, fail with
   `PinConfigRejected` with `local` = true and send nothing.
2. Choose a `request_id` at random from 1–255, different from the one this `Device` used last.
3. Send Set_Report(Feature, ID 3) with `encode_pin_config_set`.
4. Read the pin configuration back (Get_Report(Feature, ID 3)).
5. If its `request_id` differs, fail with `PinConfigConflict(expected, seen)`: another process wrote in
   between (PROTOCOL.md 6.3).
6. If its `result` is not `OK`, fail with `PinConfigRejected(result, result_gpio)` with `local` = false.
7. If its `config` differs from `config`, fail with a general error.
8. Return the report read back.

When the write succeeds and `info.pull_down_unreliable` is true while `config.pull_down_gpios` is not
empty, the library SHOULD warn in the usual way of its language (PROTOCOL.md 5.3, ADR 0004). Python raises
the warning `PullDownUnreliableWarning`; a library whose language has no such habit (Go) leaves it to the
caller, who has `pull_down_unreliable` and `pull_down_gpios`.

`update_pins({gpio: setting})` reads the pin configuration now, replaces the given pins with `with_pins`
and calls `set_pin_config`.

| Canonical name | Python | Go |
|---|---|---|
| `set_pin_config(config)` | `set_pin_config(config)` | `SetPinConfig(config)` |
| `update_pins(settings)` | `update_pins(settings)` | `UpdatePins(settings)` |

Usage:

**Python**

```python
from hidpin import PinConfig, PinConfigConflict, PinConfigRejected, PinMode, PinSetting

try:
    device.update_pins({5: PinSetting.monitor(PinMode.PULLUP, 20), 7: PinSetting.output(False)})
except PinConfigRejected as error:
    print(error.result, error.gpio, error.local)  # local: caught before sending
except PinConfigConflict:
    print("another process wrote at the same time; read again and retry")

device.set_pin_config(PinConfig.default(device.info.available))  # back to the default
```

**Go**

```go
_, err := device.UpdatePins(map[int]hidpin.PinSetting{5: hidpin.MonitorPullUp(20), 7: hidpin.Output(false)})
var rejected *hidpin.PinConfigRejectedError
var conflict *hidpin.PinConfigConflictError
switch {
case errors.As(err, &rejected):
	fmt.Println(rejected.Result, rejected.GPIO, rejected.Local) // Local: caught before sending
case errors.As(err, &conflict):
	fmt.Println("another process wrote at the same time; read again and retry")
case err != nil:
	return err
}

info, err := device.Info()
if err != nil {
	return err
}
if _, err := device.SetPinConfig(hidpin.DefaultPinConfig(info.Available)); err != nil { // back to the default
	return err
}
```

### 7.4 Status reports

| Canonical name | Meaning | Python | Go |
|---|---|---|---|
| `read_status(timeout)` | Waits for the next status report on Interrupt IN; "none" when the timeout expires | `read_status(timeout_ms)` → `None` | `ReadStatus(timeout)` → `ok` = false |
| `request_status()` | Asks for the current state with Get_Report(Input) (PROTOCOL.md 4.6) | `request_status()` | `RequestStatus()` |
| `missed_reports` | How many status reports the host failed to read so far | `missed_reports` | `MissedReports()` |

- `read_status` MUST fail on a report whose ID is not 1.
- `read_status` counts gaps in `seq`: from the second report on, `(seq - last_seq - 1) mod 65536` is
  added to `missed_reports`. A report with `HOST_REQUEST` in `reason` is not counted, and does not change
  `last_seq`. `request_status` never changes the count.

Usage:

**Python**

```python
report = device.read_status(timeout_ms=1000)  # None when nothing arrives within a second
if report is not None:
    for event in report.events:
        print(event.gpio, event.level, event.start_us(report.timestamp_us))
now = device.request_status()  # the current state, at once
print(now.levels, device.missed_reports)
```

**Go**

```go
status, ok, err := device.ReadStatus(time.Second) // ok is false when nothing arrives within a second
if err != nil {
	return err
}
if ok {
	for _, e := range status.Events {
		start, known := e.StartUS(status.TimestampUS)
		fmt.Println(e.GPIO, e.Level, start, known)
	}
}
now, err := device.RequestStatus() // the current state, at once
if err != nil {
	return err
}
fmt.Println(now.Levels, device.MissedReports())
```

### 7.5 Outputs

| Canonical name | Meaning | Python | Go |
|---|---|---|---|
| `write_outputs(mask, value)` | Sends one output command (PROTOCOL.md 7.1) | `write_outputs` | `WriteOutputs` |
| `set_outputs({gpio: high})` | Builds `mask` and `value` from the levels and sends them | `set_outputs` | `SetOutputs` |

`set_outputs` MUST fail, sending nothing, for a GPIO outside 0–29. It does not check that a pin is an
output pin: the device ignores the others (PROTOCOL.md 7.1).

Usage:

**Python**

```python
from hidpin import PinSetting

device.update_pins({7: PinSetting.output(False), 8: PinSetting.output(False)})
device.set_outputs({7: True, 8: False})  # GPIO7 HIGH, GPIO8 LOW
device.write_outputs(1 << 7, 0)  # the same with masks: GPIO7 LOW
```

**Go**

```go
if _, err := device.UpdatePins(map[int]hidpin.PinSetting{7: hidpin.Output(false), 8: hidpin.Output(false)}); err != nil {
	return err
}
if err := device.SetOutputs(map[int]bool{7: true, 8: false}); err != nil { // GPIO7 HIGH, GPIO8 LOW
	return err
}
if err := device.WriteOutputs(1<<7, 0); err != nil { // the same with masks: GPIO7 LOW
	return err
}
```

### 7.6 Polarity and ON/OFF

The host turns **pin levels** into ON/OFF. By default a monitored pin with the pull-up is **active low**
(LOW = ON, a switch to GND) and every other pin is active high.

| Canonical name | Meaning | Python | Go |
|---|---|---|---|
| `set_polarity(gpio, active_low)` | Overrides the polarity of one pin | `set_polarity` | `SetPolarity` |
| `is_active_low(gpio, config?)` | The override if there is one, otherwise whether the pin's mode in `config` is `PULLUP` | `is_active_low` | `IsActiveLow` |
| `on_off(report, config?)` | `{gpio: on}` for every monitored pin of the report | `on_off` | `OnOff` |

`config` defaults to `pin_config`.

Usage:

**Python**

```python
print(device.is_active_low(5))  # True when GPIO5 has the pull-up
device.set_polarity(5, active_low=False)  # this switch pulls the pin HIGH when pressed
report = device.request_status()
print(device.on_off(report))  # {0: False, 1: False, ...}
```

**Go**

```go
config, err := device.PinConfig()
if err != nil {
	return err
}
fmt.Println(device.IsActiveLow(5, config)) // true when GPIO5 has the pull-up
device.SetPolarity(5, false)               // this switch pulls the pin HIGH when pressed
status, err := device.RequestStatus()
if err != nil {
	return err
}
fmt.Println(device.OnOff(status, config)) // map[0:false 1:false ...]
```

## 8. Errors

| Canonical name | When | Data | Python | Go | Rust (`Error::`) |
|---|---|---|---|---|---|
| (base) | Every error of the library | — | `HidpinError` | — (plain `error`) | `Error` |
| `DeviceNotFound` | No device matches | — | `DeviceNotFound` | `ErrNotFound` | `DeviceNotFound` |
| `SeveralDevices` | No serial number and several devices | — | `SeveralDevices` | `ErrSeveralDevices` | `SeveralDevices` |
| `ProtocolVersionError` | The device speaks another protocol version | `device_version` | `ProtocolVersionError` | `*ProtocolVersionError` | `ProtocolVersion` |
| `PinConfigRejected` | Validation failed, locally or on the device | `result`, `gpio`, `local` | `PinConfigRejected` | `*PinConfigRejectedError` | `PinConfigRejected` |
| `PinConfigConflict` | Another process wrote the pin configuration in between | `expected`, `seen` | `PinConfigConflict` | `*PinConfigConflictError` | `PinConfigConflict` |
| `UnsupportedPlatform` | No backend on this operating system | — | — (`HidpinError` from `SystemBus()`) | `ErrUnsupportedPlatform` | `UnsupportedPlatform` |
| `DeviceDisconnected` | A Watch operation needs the device while it is disconnected (9.2) | — | `DeviceDisconnected` | `ErrDisconnected` | `DeviceDisconnected` |
| `WatcherClosed` | A Watch operation after the watcher stopped | — | `WatcherClosed` | `ErrWatcherClosed` | `WatcherClosed` |

- The message of `PinConfigRejected` is `the pin configuration is invalid (nothing was sent to the
  device): <message>` when `local`, otherwise `the device rejected the pin configuration: <message>`,
  `<message>` being `result_message(result)` followed by ` (GPIOn)` when `gpio` is not `RESULT_GPIO_NONE`.
- The caller MUST be able to tell the kinds in this table apart (Python: classes, Go: `errors.Is` /
  `errors.As`). Errors of the operating system or transport are passed on, wrapped if useful.

Usage:

**Python**

```python
from hidpin import Device, DeviceNotFound, HidpinError, ProtocolVersionError, SeveralDevices

try:
    with Device.open() as device:
        print(device.info.board_name)
except SeveralDevices as error:
    print(error)  # lists their serial numbers
except DeviceNotFound:
    print("plug in a board")
except ProtocolVersionError as error:
    print("update the firmware or the library:", error.device_version)
except HidpinError as error:
    print(error)  # any other error of the library
```

**Go**

```go
device, err := hidpin.Open("")
var version *hidpin.ProtocolVersionError
switch {
case errors.Is(err, hidpin.ErrSeveralDevices):
	fmt.Println(err) // lists their serial numbers
case errors.Is(err, hidpin.ErrNotFound):
	fmt.Println("plug in a board")
case errors.As(err, &version):
	fmt.Println("update the firmware or the library:", version.DeviceVersion)
case err != nil:
	return err
default:
	defer device.Close()
}
```

## 9. Watch (optional layer)

`watch(options)` starts a watcher for a long-running program and returns at once; the device need not be
connected yet. The decisions behind it are in ADR 0002.

The events MAY be offered as a blocking stream, as an asynchronous one, or both; each form behaves as this
section says.

| | Python | Go | Rust |
|---|---|---|---|
| Start | `watch(serial, target_pins=, active_low=, scan_interval=, bus=)` → `Watcher`; `async_watch(...)` → `AsyncWatcher` for asyncio | `Watch(ctx, WatchOptions)` → `*Watcher` | `watch(WatchOptions)` → `Watcher` |
| Take events | `for event in watcher`, or `watcher.get(timeout)`; `async for event in watcher`, or `await watcher.get()` | `<-w.Events()` (a channel) | `for event in &watcher`, `recv()`, or `recv_timeout(timeout)` |
| Stop | `close()`, `with` / `async with` | `Close()`, or cancelling `ctx` | `close()`, or dropping the watcher |
| Simulated bus for tests | `hidpin.testing.FakeBus`, `FakeBoard` | `hidpintest.Bus`, `Board` | `hidpin::testing::FakeBus`, `FakeBoard` |

Python runs the device on a thread of its own in both forms; `AsyncWatcher` only waits for its events on
the event loop. `scan_interval` is in seconds.

### 9.1 Options

| Canonical name | Meaning | Default |
|---|---|---|
| `serial` | The serial number of the device to watch | Empty: the first device found, and from then on that one only |
| `target_pins` | The **target pin configuration**: every GPIO, the ones not set being `UNUSED` | None: never write the pin configuration, follow the device's |
| `active_low` | `{gpio: active_low}` overrides of the polarity | Section 7.6 |
| `scan_interval` | How often to look for the device while it is disconnected | 1 second |
| `bus` | What finds and opens devices, replaceable for tests | The operating system |

`watch` MUST fail at once for a negative interval, a GPIO outside 0–29 in `active_low`, and a
`target_pins` that fails `validate_pin_config` with every GPIO counted as available.

### 9.2 Operations

| Canonical name | Meaning |
|---|---|
| `events` | The events (9.3) in the order they happened, until the watcher stops |
| `error` | Why the watcher stopped: none when it was closed or cancelled |
| `close()` | Stops it and closes the device; events not yet taken are dropped |
| `set_outputs({gpio: high})` | As `Device.set_outputs`; fails with `DeviceDisconnected` while the device is disconnected. Levels are not remembered |
| `set_target_pins(config)` | Replaces the target (9.5) |

The operations MUST be safe to call from any thread, including the one that receives the events. While
the application does not take events, the watcher stops reading the device; reports lost that way show up
as `missed` of the next `StatusReceived`.

Usage:

**Python**

```python
from hidpin import Connected, Disconnected, OnOffChange, PinConfig, PinMode, PinSetting, watch

target = PinConfig.unused().with_pins({5: PinSetting.monitor(PinMode.PULLUP, 20), 7: PinSetting.output(False)})
with watch(target_pins=target) as watcher:  # watch("SERIAL", ...) picks one of several
    for event in watcher:  # or watcher.get(timeout) from a loop of your own
        if isinstance(event, OnOffChange):
            print(f"GPIO{event.gpio}", "ON" if event.on else "OFF", event.time)
        elif isinstance(event, Connected):
            watcher.set_outputs({7: True})
        elif isinstance(event, Disconnected):
            print("unplugged; waiting for the board")
if watcher.error is not None:
    raise watcher.error
```

**Python (asyncio)**

```python
import asyncio

from hidpin import OnOffChange, async_watch


async def main():
    async with async_watch() as watcher:  # follows the pin configuration on the device
        async for event in watcher:
            if isinstance(event, OnOffChange):
                print(f"GPIO{event.gpio}", "ON" if event.on else "OFF")


asyncio.run(main())
```

**Go**

```go
target, err := hidpin.PinConfig{}.WithPins(map[int]hidpin.PinSetting{5: hidpin.MonitorPullUp(20), 7: hidpin.Output(false)})
if err != nil {
	return err
}
w, err := hidpin.Watch(ctx, hidpin.WatchOptions{TargetPins: &target}) // Serial picks one of several
if err != nil {
	return err
}
defer w.Close()
for event := range w.Events() {
	switch e := event.(type) {
	case hidpin.OnOffChange:
		fmt.Println(e.GPIO, e.On, e.Time)
	case hidpin.Connected:
		if err := w.SetOutputs(map[int]bool{7: true}); err != nil {
			log.Print(err)
		}
	case hidpin.Disconnected:
		fmt.Println("unplugged; waiting for the board")
	}
}
return w.Err()
```

### 9.3 Events

| Event | Fields | Meaning |
|---|---|---|
| `Connected` | `serial`, `path`, `info`, `pin_config`, `reconnected` | The device was opened and, with a target, made to follow it. `pin_config` is the configuration from now on; `reconnected` is false the first time only |
| `Disconnected` | `error` | The connection was lost. The watcher keeps looking for the same device |
| `ConnectFailed` | `error` | The device is there but cannot be used (permissions, protocol version, …). Reported once per cause, when the same cause happens twice in a row; the watcher keeps trying |
| `StatusReceived` | `status`, `missed`, `on` | A status report as received, including the ones the watcher asks for. `missed` counts the reports lost just before it; `on` is `on_off` of the report |
| `InitialOnOff` | `on` | ON/OFF of monitored pins the watcher did not know before: all of them after the first connection, and pins that became monitored or changed their pull. It is not a change |
| `OnOffChange` | `gpio`, `on`, `level`, `has_time`, `time`, `device_us`, `inferred` | The ON/OFF of a monitored pin changed (9.6) |
| `OutputsReset` | `gpios`, `cause` | These output pins are at their **initial output level** now. `cause`: `SUSPEND` (the report had `OUTPUT_RESET`), `RECONNECT`, `PIN_CONFIG` (a write made them outputs or changed their initial level) |
| `PinConfigChanged` | `config` | The pin configuration of the device changed. Only without a target |
| `PinConfigRestored` | `found`, `count` | Another program changed the pin configuration and the watcher wrote the target back. `count` grows each time; a count that keeps growing means a fight |

### 9.4 Connecting

When it finds its device, the watcher:

1. opens it and reads the device information (protocol version check) and the pin configuration;
2. with a target that differs, writes it (`set_pin_config`). A `PinConfigRejected` here stops the watcher
   with that error; any other failure counts as a failed attempt;
3. emits `Connected`, then `OutputsReset` with `RECONNECT` for all output pins when this is a reconnection,
   or with `PIN_CONFIG` for the pins step 2 drove;
4. asks for the current state with `request_status` and takes it as the **snapshot**. When it wrote the
   pin configuration in step 2, it takes no snapshot yet: the report with `CONFIG_CHANGED` that follows
   carries the new state and is used instead. Only when none arrives within 1 second does it take the
   snapshot.

The watcher stops with an error only when the application asked for something impossible: a target that
the board refuses, several devices and no `serial`, or no backend on this system. Everything else
(permissions, unplugging, another protocol version) is reported and retried every `scan_interval`.

### 9.5 Keeping the target

- On a report with `CONFIG_CHANGED`, the watcher reads the pin configuration. Without a target it emits
  `PinConfigChanged` when it differs from the last one seen. With a target that differs, it writes the
  target back and emits `PinConfigRestored`, at most once a second; after a `PinConfigConflict` it tries
  again a second later.
- `set_target_pins` validates against `available` once the watcher has met the device, failing with
  `PinConfigRejected` and keeping the old target. While connected it writes the new target at once; when
  that write fails for another reason, the new target stays and is written later. While disconnected, it
  is written on the next connection.
- Every write that makes pins outputs or changes their initial level emits `OutputsReset` with
  `PIN_CONFIG`. The watcher never drives outputs back to earlier levels (ADR 0002).

### 9.6 Following ON/OFF

The watcher keeps the ON/OFF of each monitored pin across connections and derives changes from each
status report, so that each change is reported exactly once:

- Reports sent before the snapshot already counted in it: an interrupt report whose `seq` is not after the
  snapshot's (compared modulo 65536) is passed on as `StatusReceived` but not used. A snapshot with
  `MORE_EVENTS`, or with `seq` = 0, is not a reference.
- After writing the pin configuration, reports are not used until the one with `CONFIG_CHANGED` arrives
  (or 1 second passes and a new snapshot is taken), as earlier reports show the old configuration
  (PROTOCOL.md 6.4).
- For each edge event of a pin it knows, it emits `OnOffChange` with the time. If the event leaves the pin
  where it already was, the opposite change was lost, and it first emits that one with `inferred` = true.
- Then, for each monitored pin, a pin it did not know goes into `InitialOnOff`, and a pin whose level
  disagrees with what it knows gets an `OnOffChange` with `inferred` = true and no time (edge events were
  lost, or the change happened while disconnected).
- Pins that stop being monitored, or whose pull changes, are forgotten and learned again.

**Time of a change.** `device_us` is the start of the change on the device clock (`start_us`). `time` is
the same moment in host time: for every report, the watcher pairs the host time it was received with its
`timestamp_us`, and keeps the pair with the smallest host-minus-device difference within the current and
the previous 30 seconds; a device time converts through that pair. A saturated or inferred change has no
time (`has_time` = false).

## 10. Testing a host library

- Every case of `protocol/vectors.json` MUST round-trip: decoding gives the fields in the file, and encoding
  those fields gives the payload in the file. The same file is checked by the C core, so passing it means
  agreeing with the firmware byte for byte.
- `validate_pin_config_payload` MUST give the `result`, `result_gpio` and `request_id` of every
  `pin_config_set` case.
- The library SHOULD come with a fake device for tests that follows PROTOCOL.md (Python: module
  `hidpin.testing`, Go: package `hidpintest`, Rust: module `hidpin::testing`; all simulate plugging in and
  out), so that applications can be tested without hardware.

Usage:

**Python**

```python
from hidpin import Device, InitialOnOff, OnOffChange, watch
from hidpin.testing import FakeBoard, FakeBus

board = FakeBoard("ABCD0123456789EF")
bus = FakeBus(board)
with Device(bus.open(bus.devices()[0])) as device:  # a Device on the simulated board
    print(device.info.board_name)
with watch(bus=bus, scan_interval=0.01) as watcher:
    next(e for e in watcher if isinstance(e, InitialOnOff))
    board.set_input(5, False)  # press the switch on GPIO5
    change = next(e for e in watcher if isinstance(e, OnOffChange))
    assert change.gpio == 5 and change.on
    bus.unplug(board)  # bus.plug(board) brings it back
```

**Go**

```go
board := hidpintest.NewBoard("ABCD0123456789EF")
bus := hidpintest.NewBus(board)
w, err := hidpin.Watch(ctx, hidpin.WatchOptions{Bus: bus, ScanInterval: 10 * time.Millisecond})
if err != nil {
	return err
}
defer w.Close()
board.SetInput(5, false) // press the switch on GPIO5
bus.Unplug(board)        // bus.Plug(board) brings it back

device := hidpin.NewDevice(hidpintest.New(hidpin.ProtocolVersion), "ABCD0123456789EF") // a Device on a fake
defer device.Close()
```

## 11. State of the existing libraries

The three libraries follow this document and provide both layers. Python offers the Watch layer in a
blocking and an asyncio form, Go as a channel, and Rust as a blocking receiver on a thread of its own.
Python and Go also come with the `hidpin` command; Rust has none.
