# hidpin protocol specification (protocol version 1)

**English** | [日本語](PROTOCOL-ja.md)

> **Status: draft (second revision, awaiting review)**

This document defines the format and the behaviour of the USB-HID reports exchanged between the **device** and the **host**.
Terms follow [CONTEXT.md](../CONTEXT.md) (written in Japanese).
This document is the authority.
The test vectors in `protocol/vectors.json` are derived from it; when the two disagree, this document wins and the vectors are fixed.

## 1. Identification on USB

| Item | Value |
|---|---|
| VID:PID | `1209:6870` (allocated to hidpin by pid.codes, <https://pid.codes/1209/6870/>) |
| Manufacturer string | `yukke.org` |
| Product string | `hidpin` |
| Serial string | The **serial number**: the board's unique ID as 16 upper-case hexadecimal digits (`[0-9A-F]{16}`, leading zeros kept) |
| HID interface | `bInterfaceClass` = 3, `bInterfaceSubClass` = 0, `bInterfaceProtocol` = 0, `bcdHID` = `0x0111` |
| Endpoints | One Interrupt IN and one Interrupt OUT, both with `wMaxPacketSize` = 64 and `bInterval` = 1 |
| Top-level collection | Usage Page `0xFF00` (vendor-defined), Usage `0x01` |

Besides the VID:PID, the **host** checks that the collection is Usage Page `0xFF00` / Usage `0x01` before opening it.
A debug build adds a CDC interface; the HID collection stays the same.

## 2. Common rules

- All multi-byte integers are **little-endian**.
- A GPIO bitmask is a `u32` whose bit *n* stands for GPIO*n*. In protocol version 1, bits 30 and 31 are always 0.
- The "offset" in the byte tables below counts from the start of the payload, **excluding** the report ID.
- Reserved fields and reserved bits are written as 0 by the sender and ignored by the receiver.
- All times are microseconds (µs) since the **device** was powered on.

## 3. Reports

| Report ID | Type | Name | Payload length | Path |
|---|---|---|---|---|
| `0x01` | Input | **Status report** | 63 | Interrupt IN, and the reply to Get_Report(Input) |
| `0x02` | Feature | **Device information** | 63 | Get_Report(Feature) |
| `0x03` | Feature | **Pin configuration** | 63 | Get_Report(Feature) / Set_Report(Feature) |
| `0x04` | Output | Output command | 8 | Interrupt OUT (Set_Report(Output) is treated the same) |

Input and Feature reports are 64 bytes long including the report ID.
On Windows, the correct buffer for reading a Feature report is said to be "the length of the longest Feature report in the collection", so all Feature reports have the same length.

### 3.1 Lengths passed to host APIs

With hidapi / hidraw, the payload is preceded by one byte holding the report ID.

| Operation | Length passed or received (including the report ID) |
|---|---|
| Reading Interrupt IN (`read`) | 64 |
| `get_input_report(0x01, …)` | 64 |
| `get_feature_report(0x02 or 0x03, …)` | 64 |
| `send_feature_report` (ID `0x03`) | 64 |
| Sending an output command (`write`) | 9 |

### 3.2 Report descriptor (47 bytes)

```
Usage Page (0xFF00)            06 00 FF
Usage (0x01)                   09 01
Collection (Application)       A1 01
  Report ID (1)                85 01
  Usage (0x10)                 09 10
  Logical Minimum (0)          15 00
  Logical Maximum (255)        26 FF 00
  Report Size (8)              75 08
  Report Count (63)            95 3F
  Input (Data,Var,Abs)         81 02
  Report ID (2)                85 02
  Usage (0x20)                 09 20
  Report Count (63)            95 3F
  Feature (Data,Var,Abs)       B1 02
  Report ID (3)                85 03
  Usage (0x30)                 09 30
  Report Count (63)            95 3F
  Feature (Data,Var,Abs)       B1 02
  Report ID (4)                85 04
  Usage (0x40)                 09 40
  Report Count (8)             95 08
  Output (Data,Var,Abs)        91 02
End Collection                 C0
```

## 4. Status report (ID `0x01`, Input, 63 bytes)

| Offset | Type | Name | Contents |
|---|---|---|---|
| 0 | `u16` | `seq` | Sequence number (4.3) |
| 2 | `u8` | `reason` | Bit flags saying why the report was sent (4.1) |
| 3 | `u8` | `flags` | Auxiliary flags (4.2) |
| 4 | `u8` | `event_count` | Number of **edge events** included (0–7) |
| 5 | `u8`×3 | — | Reserved |
| 8 | `u32` | `monitored` | Bitmask of the **monitored pins** |
| 12 | `u32` | `outputs` | Bitmask of the **output pins** |
| 16 | `u32` | `levels` | The **pin levels** of the **monitored pins** and the **output levels** of the **output pins** (HIGH = 1). Other bits are 0 |
| 20 | `u64` | `timestamp_us` | When the contents of this **status report** were assembled |
| 28 | 5 bytes × 7 | `events[0..6]` | **Edge events** (4.4). The first `event_count` are valid; the rest are 0 |

### 4.1 `reason` (bit flags)

When several reasons arise while a report waits to be sent, all of the matching bits are set.
A **status report** sent on Interrupt IN has at least one bit set.

| Bit | Value | Name | Meaning |
|---|---|---|---|
| 0 | `0x01` | `LEVEL_CHANGED` | Includes one or more **edge events** |
| 1 | `0x02` | `OUTPUT_APPLIED` | An output command (ID `0x04`) was applied. Set even when no **output level** changed |
| 2 | `0x04` | `CONFIG_CHANGED` | The **pin configuration** changed (6.4) |
| 3 | `0x08` | `PERIODIC` | 1000 ms have passed since the last Interrupt IN transfer |
| 4 | `0x10` | `OUTPUT_RESET` | The **output pins** were returned to their **initial output levels** because USB was disconnected, suspended or reset (7.2) |
| 7 | `0x80` | `HOST_REQUEST` | This is the reply to Get_Report(Input). When this bit is set, no other bit is |

### 4.2 `flags`

| Bit | Name | Meaning |
|---|---|---|
| 0 | `OVERFLOW` | Since the previous **status report**, the **device** has dropped **edge events** it could not hold (4.5) |
| 1 | `MORE_EVENTS` | Unsent **edge events** remain, and more **status reports** follow |

### 4.3 `seq`

- The **device** starts with `next_seq` = 0.
- When a **status report** is successfully handed to Interrupt IN, its `seq` is `next_seq`, and `next_seq` is incremented (0 follows 65535).
- The reply to Get_Report(Input) does not change `next_seq`; it carries the `seq` most recently handed to Interrupt IN (0 if none has been sent).
- A USB bus reset or a reconnection does not reset `next_seq`; only a firmware restart does.
- A gap in `seq` means that the **host** missed a **status report** that was sent. **Edge events** dropped by the **device** are signalled by `OVERFLOW`.

### 4.4 Edge event (5 bytes)

| Offset | Type | Name | Contents |
|---|---|---|---|
| 0 | `u8` | `pin` | Bits 0–4: GPIO number. Bits 5–6: reserved. Bit 7: the **pin level** after the change (HIGH = 1) |
| 1 | `u32` | `age_us` | How far back from this **status report**'s `timestamp_us` the change started. `0xFFFFFFFF` means "at least this long ago" (saturated) |

The time the change started is `timestamp_us - age_us` (unless saturated).
`events` are ordered by the time the change started, oldest first.

### 4.5 Sending rules

- **Edge events** enter a queue in the **device** (32 entries) in the order they are confirmed.
- An **edge event** confirmed while the queue is full is dropped, and `OVERFLOW` is scheduled to be set.
- When a **status report** is assembled, up to 7 events are taken from the head of the queue into `events`.
  - If events remain in the queue, `MORE_EVENTS` is set, and the next **status report** is sent as soon as the endpoint is free.
  - The bits of `levels` for **monitored pins** reflect the changes up to the last **edge event** included in that **status report**.
    **Edge events** still in the queue are not reflected.
  - In the **status report** that empties the queue, `levels` matches the current confirmed **pin levels**.
    `OVERFLOW` is set on this **status report**. The effect of the dropped **edge events** is reflected in this `levels` as well.
- The bits of `levels` for **output pins**, and `monitored` / `outputs`, always hold the values at the time of assembly.
- Reasons that arise while the endpoint is busy and cannot send are combined into one **status report** (`reason` is ORed).
- Nothing is sent while USB is suspended (remote wake-up is not used).

### 4.6 Reply to Get_Report(Input)

- `reason` = `HOST_REQUEST` and `event_count` = 0. Nothing is taken from the queue.
- `levels` holds the current confirmed **pin levels** and **output levels**. If unsent **edge events** are in the queue, `MORE_EVENTS` is set.
- `OVERFLOW` is not set (it is conveyed on Interrupt IN).

## 5. Device information (ID `0x02`, Feature, 63 bytes)

| Offset | Type | Name | Contents |
|---|---|---|---|
| 0 | `u8` | `protocol_version` | The version of this document. Currently `1` |
| 1 | `u8` | `fw_major` | Firmware version (major) |
| 2 | `u8` | `fw_minor` | Firmware version (minor) |
| 3 | `u8` | `fw_patch` | Firmware version (patch) |
| 4 | `u8` | `board` | Board type (5.1) |
| 5 | `u8` | `gpio_count` | Number of entries in the **pin configuration**. Always `30` in protocol version 1 |
| 6 | `u16` | `periodic_interval_ms` | Interval of `PERIODIC` reports. Currently `1000` |
| 8 | `u32` | `available` | Bitmask of the **available GPIOs** |
| 12 | `u8` | `events_per_report` | Maximum number of **edge events** in one **status report**. `7` in protocol version 1 |
| 13 | `u8` | `event_queue_size` | Size of the **edge event** queue. Currently `32` |
| 14 | `u8`×49 | — | Reserved |

- Contents sent with Set_Report are ignored.
- If `protocol_version` differs from the version the **host** supports, the **host** stops communicating and reports an error.

### 5.1 `board`

| Value | Board | `available` |
|---|---|---|
| 1 | Raspberry Pi Pico | `0x1C7FFFFF` (GPIO0–22, 26–28) |
| 2 | Adafruit QT Py RP2040 | `0x3FD00078` (GPIO3–6, 20, 22–29) |

## 6. Pin configuration (ID `0x03`, Feature, 63 bytes)

| Offset | Type | Name | Contents |
|---|---|---|---|
| 0 | `u8` | `result` | Get: the result of the last Set processed (6.3). Set: write 0 (ignored) |
| 1 | `u8` | `result_gpio` | Get: the GPIO number when `result` is an error about a GPIO, otherwise `0xFF`. Set: write `0xFF` (ignored) |
| 2 | `u8` | `request_id` | Set: a value from 1 to 255 chosen by the **host**. Get: the `request_id` of the last Set processed (0 if no Set has been received since start-up) |
| 3 + 2*n | `u8` | `mode[n]` | How GPIO*n* is used (6.1). n = 0..29 |
| 4 + 2*n | `u8` | `param[n]` | The parameter for GPIO*n* (6.1) |

### 6.1 `mode` and `param`

| `mode` | Use of the GPIO | Meaning of `param` |
|---|---|---|
| 0 | Unused (input disabled, no pull, output disabled) | Must be 0 |
| 1 | Monitored, no pull | **Debounce time** (0–255 ms) |
| 2 | Monitored, pull-up | **Debounce time** (0–255 ms) |
| 3 | Monitored, pull-down | **Debounce time** (0–255 ms) |
| 4 | Output | **Initial output level** (0 = LOW, 1 = HIGH) |

In the **default pin configuration**, every **available GPIO** has `mode` = 2 and `param` = 20, and every other GPIO has `mode` = 0 and `param` = 0.

### 6.2 Accepting a Set

- A Set_Report longer than 64 bytes including the report ID is STALLed on USB, and nothing changes.
- Any other Set_Report always succeeds on USB (an HID SET_REPORT cannot be refused because of its contents).
  Errors in the contents are reported through `result`.

### 6.3 Validation and `result`

The **device** validates a Set with the steps below and records the first error found in `result`.
If there is an error, none of the **pin configuration** is applied, and the previous **pin configuration** stays.
Whether or not there is an error, `request_id` is recorded (as 0 when the payload is shorter than 3 bytes).

```
if payload length != 63:          result = 1; result_gpio = 0xFF; stop
for n in 0..29:
    if mode[n] > 4:                result = 2; result_gpio = n; stop
    if GPIOn is not an available GPIO and mode[n] != 0:
                                   result = 3; result_gpio = n; stop
    if mode[n] == 0 and param[n] != 0:
                                   result = 4; result_gpio = n; stop
    if mode[n] == 4 and param[n] > 1:
                                   result = 5; result_gpio = n; stop
result = 0; result_gpio = 0xFF; apply
```

| `result` | Meaning |
|---|---|
| 0 | Applied (also 0 when no Set has been received since start-up) |
| 1 | The payload length is not 63 (hidapi on Windows pads a short Set with zeros, so some operating systems never show this) |
| 2 | `mode` is outside 0–4 |
| 3 | `mode` ≠ 0 for a GPIO that is not an **available GPIO** |
| 4 | `param` ≠ 0 for a GPIO with `mode` = 0 |
| 5 | `param` > 1 for a GPIO with `mode` = 4 |

In the reply to a Get, `result` / `result_gpio` / `request_id` describe the last Set processed, and `mode` / `param` describe the **pin configuration** currently applied.
After receiving a Set, the **device** must have these up to date by the next Get.

The **host** decides whether a Set succeeded with the steps below.

1. Choose a `request_id` from 1 to 255 that differs from the previous one, and send the Set.
2. Do a Get.
3. If `request_id` does not match, assume that another **host** process sent a Set at the same time, and fail with a conflict.
4. If `result` ≠ 0, fail as rejected.
5. If `mode` / `param` do not match what was sent, fail.

### 6.4 Behaviour when applying

Each GPIO is handled according to its `mode` / `param` before and after the change.

| Kind of change | Behaviour |
|---|---|
| `mode` and `param` both unchanged | Nothing happens. A **monitored pin** keeps its confirmed **pin level** and the debouncing in progress; an **output pin** keeps its current **output level** |
| Still a **monitored pin**, and only `param` (the **debounce time**) changes | The confirmed **pin level** is kept, and the debouncing in progress is discarded |
| Becomes a **monitored pin** from something else, or stays one with a different pull | The output is disabled and the pull is set. After waiting about 1 ms, the value read becomes the first confirmed **pin level**. This confirmation is not an **edge event** |
| Becomes an **output pin** from something else, or stays one with a different **initial output level** | The **initial output level** is driven |
| Becomes unused (`mode` = 0) | Input, pull and output are all disabled |

- For a GPIO that stops being a **monitored pin**, the debouncing in progress is discarded. Its **edge events** already in the queue are not dropped.
- `result` / `request_id` / `mode` / `param` are updated when the Set is received.
  `monitored` / `outputs` / `levels` are updated together after the wait of about 1 ms above.
  Until then, Get_Report(Input) returns the contents from before the change.
- Only when the applied **pin configuration** differs from the previous one, a `CONFIG_CHANGED` **status report** is sent after the update.

## 7. Outputs

### 7.1 Output command (ID `0x04`, Output, 8 bytes)

| Offset | Type | Name | Contents |
|---|---|---|---|
| 0 | `u32` | `mask` | Bitmask of the GPIOs to change |
| 4 | `u32` | `value` | The new **output levels** (HIGH = 1). Only GPIOs whose bit is set in `mask` are affected |

- An output command whose payload length is not 8 is ignored entirely (no **status report** is sent either).
- Bits of `mask` for GPIOs that are not **output pins** are ignored.
- The **output levels** of the affected **output pins** change at the same time.
- After applying, an `OUTPUT_APPLIED` **status report** is sent, even when no **output pin** was affected.

### 7.2 USB disconnection, suspend and bus reset

- When one of these is detected, every **output pin** returns to its **initial output level**. The **pin configuration** does not change.
- Only if there was at least one **output pin**, `OUTPUT_RESET` is set on the next **status report** sent.
- That **status report** is sent right after reconnection, so a **host** that has not opened the device by then does not receive it.
  After reconnecting, the **host** sets the **output levels** again if needed. The current **output levels** can be read from `levels`.

## 8. Debouncing

- The **device** detects electrical changes on **monitored pins** with GPIO interrupts and records the time of each change in µs.
- When the electrical value has differed from the confirmed **pin level** for at least `param` (the **debounce time**) milliseconds since the last change, the **pin level** is updated to that value and one **edge event** is created.
  - The confirmation happens within 1 ms of the condition being met.
  - The "time the change started" of the **edge event** is the time of that last change.
- If the value goes back to the confirmed one in the meantime, the elapsed time is discarded, and no **edge event** is created.
- When the **debounce time** is 0, every electrical change detected updates the **pin level** and creates an **edge event**.

## 9. Versioning

- An incompatible change (moving a field, changing its meaning or length, changing a report ID, or adding GPIOs) increments `protocol_version`.
- In every version, the report ID of the **device information** (`0x02`), its length (64 bytes including the ID) and `protocol_version` at offset 0 stay the same.
  The **host** uses them to tell the version.
- Giving meaning to reserved fields, or adding values to `reason` / `flags` / `board`, is a compatible change and does not increment `protocol_version`.
  The **host** keeps communicating when it receives unknown bits or values.
- Chips with 31 or more GPIOs (such as the RP2350B) are left to protocol version 2 or later.
