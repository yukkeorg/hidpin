# hidpin ホストライブラリ API（版 1）

[English](HOST_API.md) | **日本語**

この文書は、hidpin の**ホストライブラリ**が言語を問わず提供する API を定める。
新しい言語でホストライブラリを作るときに従う仕様であり、既存の Python 版（[`python/`](../python)）と
Go 版（[`hidpin/`](../hidpin)）を照らし合わせる基準でもある。

- 通信形式は [PROTOCOL-ja.md](PROTOCOL-ja.md) が定め、バイト列と**デバイス**の振る舞いについてはそちらが正である。
  この文書は、その上でホストライブラリが何を提供するかを定める。
- 太字の用語は用語集 [CONTEXT.md](../CONTEXT.md) のものである。
- 大文字の MUST、MUST NOT、SHOULD、SHOULD NOT、MAY は、RFC 2119 と RFC 8174 の意味で用いる。
  本文では「しなければならない（MUST）」のように併記する。

英語版 [HOST_API.md](HOST_API.md) が正であり、この文書はその翻訳である。

## 1. 範囲と準拠

API は 2 つの層からなる。

| 層 | 内容 | 要件 | Python | Go |
|---|---|---|---|---|
| **Core** | 定数、型、全レポートの符号化と復号、デバイスの検索と接続、`Device` の操作（3〜8 章） | ホストライブラリはすべて提供しなければならない（MUST） | あり | あり |
| **Watch** | 常駐プログラム向けの監視役。デバイスを待ち、開き直し、ピン設定を目標に保ち、イベントを通知する（9 章） | 提供してもよい（MAY）。提供する場合は 9 章のとおりに振る舞わなければならない（MUST） | あり | あり |

ホストライブラリは独自の関数を足してもよい（MAY。コマンドラインツールはこの文書の対象外）。
ただし、ここで名前を挙げた操作の意味を変えてはならない（MUST NOT）。

ホストライブラリは [`protocol/vectors.json`](../protocol/vectors.json) のすべての事例に合格しなければならない（MUST、10 章）。

## 2. 名前の付け方

操作と型にはそれぞれ**正規名**がある。この文書では、関数とフィールドを `snake_case`、型を `PascalCase` で書く。
ホストライブラリはその言語の慣習に従って綴り（例: Python は `read_status`、Go は `ReadStatus`）、
どの言語でもコードが同じように読めるよう、語はそろえるべきである（SHOULD）。
この文書の表には、既存のライブラリでの名前を示す。

- 失敗しうる操作は、その言語の普通のやり方でエラーを返すか送出する。Python は例外、Go は `error` の戻り値。
- 省略可能な引数がない言語では、この文書で省略可能とした引数は必須の引数か、別の関数になる（Go: `Device.OnOff(status, config)`）。
- GPIO のビットマスクは符号なし 32 ビット整数で、ビット *n* が GPIO*n* を表す（PROTOCOL-ja.md 2）。
- GPIO 番号は 0〜29 の整数である。
- デバイス上の時刻は、電源投入からのマイクロ秒で、符号なし 64 ビット整数である。

ほとんどの節の終わりに、Python と Go の使い方の例を載せる。開いたデバイスが要る例では、それを `device` と呼ぶ（7.1）。
Go の例は `error` を返す関数の本体であり、Watch の例ではさらに `context.Context` の `ctx` があるものとする。
文の後ろのコメントは、その文が表示する内容か、その文の働きを示す。

## 3. 定数

| 正規名 | 値 | Python | Go |
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

使い方:

**Python**

```python
from hidpin import protocol

print(protocol.GPIO_COUNT, protocol.DEFAULT_DEBOUNCE_MS)  # 30 20
```

**Go**

```go
fmt.Println(hidpin.GPIOCount, hidpin.DefaultDebounceMS) // 30 20
```

## 4. 型

以下の型はすべて値である。レポートを復号すると新しい値ができ、その後は変化しない。
変更できないレコード型を持つ言語では、それを使うべきである（SHOULD）。

### 4.1 列挙とフラグ

列挙は、知らない値で失敗せず、その値を保持する（PROTOCOL-ja.md 9: 新しいデバイスは、このライブラリが知らない値を送りうる）。

| 正規名 | 種類 | 値 | Python | Go |
|---|---|---|---|---|
| `Reason` | フラグ | `LEVEL_CHANGED` 0x01、`OUTPUT_APPLIED` 0x02、`CONFIG_CHANGED` 0x04、`PERIODIC` 0x08、`OUTPUT_RESET` 0x10、`HOST_REQUEST` 0x80 | `Reason`（`IntFlag`） | `Reason`、`Reason*` |
| `StatusFlags` | フラグ | `OVERFLOW` 0x01、`MORE_EVENTS` 0x02 | `StatusFlags` | `StatusFlags`、`Flag*` |
| `PinMode` | 列挙 | `UNUSED` 0、`INPUT` 1（プルなし）、`PULLUP` 2、`PULLDOWN` 3、`OUTPUT` 4 | `PinMode` | `PinMode`、`Mode*` |
| `Result` | 列挙 | `OK` 0、`BAD_LENGTH` 1、`BAD_MODE` 2、`UNAVAILABLE_GPIO` 3、`UNUSED_WITH_PARAM` 4、`BAD_OUTPUT_LEVEL` 5 | `Result` | `Result`、`Result*` |
| `Board` | 列挙 | `OTHER` 0、`PICO` 1、`QTPY_RP2040` 2、`PICO2` 3 | `Board` | `Board`、`Board*` |
| `Chip` | 列挙 | 0 = 申告なし、`RP2040` 1、`RP2350A` 2 | `Chip` | `Chip`、`Chip*` |
| `Quirks` | フラグ | `PULL_DOWN_UNRELIABLE` 0x01 | `Quirk` | `Quirks`、`QuirkPullDownUnreliable` |

必須の補助:

- `PinMode.is_input`: `INPUT`、`PULLUP`、`PULLDOWN` のとき真。
- `result_message(result)`（Python は `protocol.result_message`、Go は `Result.Message()`）: 各 `Result` の英語の 1 行。次の表のとおり。知らない値は `result N`。

  | `Result` | メッセージ |
  |---|---|
  | `OK` | `applied` |
  | `BAD_LENGTH` | `the payload is not 63 bytes long` |
  | `BAD_MODE` | `mode is outside 0-4` |
  | `UNAVAILABLE_GPIO` | `the pin is not an available GPIO` |
  | `UNUSED_WITH_PARAM` | `an unused pin was given a parameter` |
  | `BAD_OUTPUT_LEVEL` | `the initial output level is neither 0 nor 1` |

- `board_name(board, sdk_board)`: `PICO` は `Raspberry Pi Pico`、`QTPY_RP2040` は `Adafruit QT Py RP2040`、
  `PICO2` は `Raspberry Pi Pico 2`。それ以外の値では、`sdk_board` が空でなければそれ、空なら 0 は `other board`、
  ほかは `unknown board N`。
- `chip_name(chip)`: 0 は `not reported`、`RP2040`、`RP2350A`、それ以外は `unknown chip N`。

使い方:

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

### 4.2 `EdgeEvent`（PROTOCOL-ja.md 4.4）

| フィールド・操作 | 意味 |
|---|---|
| `gpio` | GPIO 番号（`pin` バイトのビット 0〜4） |
| `level` | 変化後の**ピンレベル**（真 = HIGH） |
| `age_us` | 変化からレポートの `timestamp_us` までのマイクロ秒 |
| `saturated` | `age_us` = `SATURATED_AGE_US` のとき真 |
| `start_us(timestamp_us)` | `timestamp_us - age_us`。飽和しているときは「不明」（Python は `None`、Go は `ok` = false） |

使い方:

**Python**

```python
from hidpin import EdgeEvent

event = EdgeEvent(gpio=5, level=False, age_us=20_000)
print(event.start_us(4_000_000))  # 3980000: 通知の 20 ms 前
print(EdgeEvent(5, False, 0xFFFFFFFF).start_us(4_000_000))  # None: 古すぎて分からない
```

**Go**

```go
event := hidpin.EdgeEvent{GPIO: 5, Level: false, AgeUS: 20_000}
start, ok := event.StartUS(4_000_000)
fmt.Println(start, ok) // 3980000 true: 通知の 20 ms 前
```

### 4.3 `StatusReport`（PROTOCOL-ja.md 4）

Python は `StatusReport`、Go は `Status`。

| フィールド・操作 | 意味 |
|---|---|
| `seq`、`reason`、`flags`、`monitored`、`outputs`、`levels`、`timestamp_us` | PROTOCOL-ja.md 4 のとおり |
| `events` | **エッジイベント**。最大 7 個、順序どおり |
| `overflow`、`more_events` | そのフラグが立っているか |
| `level(gpio)`、`is_monitored(gpio)`、`is_output(gpio)` | `levels`、`monitored`、`outputs` の該当ビット |

使い方:

**Python**

```python
report = device.request_status()
print(report.seq, report.reason, report.timestamp_us)
print(report.is_monitored(5), report.level(5))  # True True: 監視中、HIGH
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
fmt.Println(status.IsMonitored(5), status.Level(5)) // true true: 監視中、HIGH
if status.Overflow() {
	fmt.Println("the device dropped edge events")
}
```

### 4.4 `DeviceInfo`（PROTOCOL-ja.md 5）

| フィールド・操作 | 意味 |
|---|---|
| `protocol_version`、`firmware`（major、minor、patch）、`board`、`gpio_count`、`periodic_interval_ms`、`available`、`events_per_report`、`event_queue_size`、`chip`、`chip_revision`、`quirks`、`sdk_board` | PROTOCOL-ja.md 5 のとおり。`chip`、`chip_revision`、`quirks`、`sdk_board` の既定値は、古いファームウェアが送るのと同じ 0 または空 |
| `firmware_version` | `"major.minor.patch"` |
| `available_gpios` | `available` に含まれる GPIO 番号。昇順 |
| `board_name` | `board_name(board, sdk_board)` |
| `chip_name` | `chip_name(chip)`（Go は `Chip.Name()`） |
| `pull_down_unreliable` | `quirks` に `PULL_DOWN_UNRELIABLE` があるか |

`sdk_board` は、最初の 0 バイトまでを ASCII として復号する。ASCII 以外のバイトは 1 バイトずつ U+FFFD にする。

使い方:

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

### 4.5 `PinSetting` と `PinConfig`（PROTOCOL-ja.md 6）

`PinSetting` は 1 本の GPIO の使い方で、`mode`（`PinMode`）と `param`（0〜255）からなる。

| 生成 | 結果 | Python | Go |
|---|---|---|---|
| `unused()` | `UNUSED`、0 | `PinSetting.unused()` | `Unused()` |
| `monitor(mode, debounce_ms)` | 入力のモードと**チャタリング除去時間**。入力でないモードはエラー | `PinSetting.monitor(mode, debounce_ms)` | `MonitorNoPull`、`MonitorPullUp`、`MonitorPullDown` |
| `output(initial_high)` | `OUTPUT`、1 または 0 | `PinSetting.output(level)` | `Output(initialHigh)` |

`PinSetting` は `is_monitored` と `is_output`、および CLI が使う文字列表現（`off`、`nopull:N`、`pullup:N`、
`pulldown:N`、`out:low`、`out:high`）も持つ。

`PinConfig` はちょうど `GPIO_COUNT` 個の設定を持ち、GPIO 番号で引く。

| 操作 | 意味 |
|---|---|
| `unused()` | すべての GPIO が `UNUSED`。**目標ピン設定**を組み立てる起点（Python は `PinConfig.unused()`、Go はゼロ値 `PinConfig{}`） |
| `default(available)` | 利用可能な GPIO は `PULLUP` と `DEFAULT_DEBOUNCE_MS`、それ以外は `UNUSED`（PROTOCOL-ja.md 6.1） |
| `config[gpio]` | 1 本の GPIO の設定 |
| `with_pins({gpio: setting})` | 一部のピンを置き換えた複製。0〜29 の外の GPIO はエラー |
| `monitored_mask`、`outputs_mask` | 監視ピンと出力ピンのビットマスク |
| `pull_down_gpios` | モードが `PULLDOWN` の GPIO。昇順 |

2 つの `PinConfig` は、すべての設定が等しいとき等しい。

使い方:

**Python**

```python
from hidpin import PinConfig, PinMode, PinSetting

config = PinConfig.unused().with_pins(
    {
        5: PinSetting.monitor(PinMode.PULLUP, 20),  # GND へのスイッチ
        6: PinSetting.monitor(PinMode.INPUT, 0),  # プルなし、チャタリング除去なし
        7: PinSetting.output(False),  # LOW で始まり、LOW に戻る
    }
)
print(config[5], config[7])  # pullup:20 out:low
print(hex(config.monitored_mask), hex(config.outputs_mask))  # 0x60 0x80
print(PinConfig.default(0x1C7FFFFF)[0])  # pullup:20（デバイスの起動時と同じ）
```

**Go**

```go
config, err := hidpin.PinConfig{}.WithPins(map[int]hidpin.PinSetting{
	5: hidpin.MonitorPullUp(20), // GND へのスイッチ
	6: hidpin.MonitorNoPull(0),  // プルなし、チャタリング除去なし
	7: hidpin.Output(false),     // LOW で始まり、LOW に戻る
})
if err != nil {
	return err
}
fmt.Println(config[5], config[7])                                     // pullup:20 out:low
fmt.Printf("%#x %#x\n", config.MonitoredMask(), config.OutputsMask()) // 0x60 0x80
fmt.Println(hidpin.DefaultPinConfig(0x1C7FFFFF)[0])                   // pullup:20（デバイスの起動時と同じ）
```

### 4.6 `PinConfigReport`

`result`（`Result`）、`result_gpio`、`request_id`、`config`（`PinConfig`）。PROTOCOL-ja.md 6 のとおり。

使い方:

**Python**

```python
report = device.get_pin_config()
print(report.result, report.result_gpio, report.request_id)  # 直前の書き込みの結果
print(report.config[5])
```

**Go**

```go
report, err := device.GetPinConfig()
if err != nil {
	return err
}
fmt.Println(report.Result, report.ResultGPIO, report.RequestID) // 直前の書き込みの結果
fmt.Println(report.Config[5])
```

## 5. 符号化と検証

これらの関数は入出力を行わない。ペイロードは、PROTOCOL-ja.md と同じくレポート ID のバイトを含まない。

| 正規名 | Python（`hidpin.protocol`） | Go |
|---|---|---|
| `decode_status(payload)` / `encode_status(report)` | `decode_status`、`encode_status` | `DecodeStatus`、`Status.Encode` |
| `decode_device_info(payload)` / `encode_device_info(info)` | `decode_device_info`、`encode_device_info` | `DecodeDeviceInfo`、`DeviceInfo.Encode` |
| `decode_pin_config(payload)` / `encode_pin_config(report)` | `decode_pin_config`、`encode_pin_config` | `DecodePinConfig`、`PinConfigReport.Encode` |
| `encode_pin_config_set(config, request_id)` | `encode_pin_config_set` | `EncodePinConfigSet` |
| `encode_output(mask, value)` / `decode_output(payload)` | `encode_output`、`decode_output` | `EncodeOutput`、`DecodeOutput` |
| `validate_pin_config(config, available)` | `validate_pin_config` | `ValidatePinConfig` |
| `validate_pin_config_payload(payload, available)` | `validate_pin_config_payload` | `ValidatePinConfigPayload` |
| `mask_to_gpios(mask)` | `mask_to_gpios` | `MaskToGPIOs` |

- 復号は、長さの違うペイロード、7 を超える `event_count`、4 を超える `mode` で失敗しなければならない（MUST）。
- `encode_pin_config_set` は `result` = 0、`result_gpio` = `0xFF` を書き、`request_id` が 1〜255 の外なら失敗する。
- `validate_pin_config_payload` は、デバイスの検査を同じ順序で繰り返し（PROTOCOL-ja.md 6.3）、
  結果、それが指す GPIO（なければ `RESULT_GPIO_NONE`）、`request_id` を返す。
  `validate_pin_config` は `PinConfig` について同じことをする。
- 符号化はライブラリ自身だけでなくテストや偽のデバイスにも要るので、公開しなければならない（MUST）。

使い方:

**Python**

```python
from hidpin import protocol

print(protocol.encode_output(1 << 7, 1 << 7).hex())  # 8000000080000000: GPIO7 HIGH
report = protocol.decode_status(bytes(63))  # レポート ID を除いたペイロード
result, gpio = protocol.validate_pin_config(config, 0x1C7FFFFF)
print(result, gpio)  # 0 255: OK、GPIO なし
print(protocol.mask_to_gpios(0x60))  # [5, 6]
```

**Go**

```go
fmt.Printf("%x\n", hidpin.EncodeOutput(1<<7, 1<<7)) // 8000000080000000: GPIO7 HIGH
status, err := hidpin.DecodeStatus(make([]byte, hidpin.PayloadLen)) // レポート ID を除いたペイロード
if err != nil {
	return err
}
result, gpio := hidpin.ValidatePinConfig(config, 0x1C7FFFFF)
fmt.Println(status.Seq, result, gpio) // 0 0 255: OK、GPIO なし
fmt.Println(hidpin.MaskToGPIOs(0x60)) // [5 6]
```

## 6. デバイスの検索

| 正規名 | 意味 | Python | Go |
|---|---|---|---|
| `usb_ids()` | 探すベンダー ID とプロダクト ID | `device.usb_ids()` | `USBIDs()` |
| `DeviceEntry` | 見つかった 1 台。`path`、`serial`、`manufacturer`、`product` | `DeviceEntry` | `Entry` |
| `find_devices()` | つながっている hidpin デバイスの一覧 | `find_devices()` | `FindDevices()` |
| `choose_device(entries, serial)` | 1 台を選ぶ（下記） | `choose_device()` | `ChooseDevice()` |

- `usb_ids` は環境変数 `HIDPIN_VID` と `HIDPIN_PID` を読み、既定値より優先しなければならない（MUST）。
  値は 10 進数か `0x` 付きの数で、それ以外や `0xFFFF` を超える値はエラー。空または未設定は既定値を意味する。
- `find_devices` は、これらの ID を持ち、トップレベルのコレクションが Usage Page `0xFF00`、Usage `0x01` の HID インターフェースを列挙する（PROTOCOL-ja.md 1）。
  バックエンドが Usage を知る手段を持たない場合（レポートディスクリプタを読めないときなど）は ID だけに頼り、代わりに開くときのプロトコル版の確認で見分ける。
  Linux では、レポートディスクリプタは `/sys/class/hidraw/hidrawN/device/report_descriptor` にあり、
  最初の Collection (Application) の時点で有効な Usage Page と Usage を使う。一覧はシリアル番号順、同じならパス順に並べるべきである（SHOULD）。
- `choose_device` は、シリアル番号が `serial` と等しいものを選ぶ。`serial` がなければ唯一のものを選ぶ。
  一致がなければ「見つからない」で、`serial` がなく複数あれば「複数ある」（それらのシリアル番号を挙げる）で失敗する。
- ライブラリはバックエンドを自分で選ぶべきであり（SHOULD。Linux は `/dev/hidraw*`、それ以外は hidapi）、
  利用者に選ばせてもよい（MAY。Python は `HIDPIN_BACKEND` = `hidraw` / `hidapi`）。

使い方:

**Python**

```python
from hidpin import choose_device, find_devices

entries = find_devices()  # HIDPIN_VID / HIDPIN_PID で USB ID を変えられる
for entry in entries:
    print(entry.serial, entry.path)
entry = choose_device(entries)  # 唯一の 1 台。choose_device(entries, "シリアル番号") で選ぶ
```

**Go**

```go
entries, err := hidpin.FindDevices() // HIDPIN_VID / HIDPIN_PID で USB ID を変えられる
if err != nil {
	return err
}
for _, e := range entries {
	fmt.Println(e.Serial, e.Path)
}
entry, err := hidpin.ChooseDevice(entries, "") // 唯一の 1 台。シリアル番号を渡すとその 1 台を選ぶ
if err != nil {
	return err
}
fmt.Println(entry.Serial)
```

## 7. `Device`

`Device` は開いた 1 台である。複数のスレッドから同時に使えなくてもよいが、使えるかどうかを明記しなければならない（MUST）。

### 7.1 開く・閉じる

| 正規名 | 意味 | Python | Go |
|---|---|---|---|
| `open(serial?)` | デバイスを探し、`choose_device` で 1 台を選んで開く | `Device.open(serial)` | `Open(serial)` |
| `open_path(path, serial?)` | 分かっているデバイスノードを開く | `Device.open(path=...)` | `OpenPath(path, serial)` |
| `close()` | 閉じる | `close()`、`with` | `Close()` |
| `serial` | 開いたときのシリアル番号 | `serial` | `Serial` |

- 開くときは、ほかのレポートを送る前にデバイス情報を読み、`protocol_version` が 1 でなければ `ProtocolVersionError` で失敗しなければならない（MUST）。
  失敗したときは、開いたものを閉じる。
- 権限がなくて開けないときは、エラーに udev ルールの入れ方を書くべきである（SHOULD）。
- 呼び出し側の通り道（テストでの偽のデバイス）の上に `Device` を作る手段を用意すべきである（SHOULD）。
  Python は hidapi のメソッドを持つ任意のオブジェクトを、Go は `Transport` を受け取る。

使い方:

**Python**

```python
from hidpin import Device

with Device.open() as device:  # つながっている唯一のボード
    print(device.serial)
device = Device.open("ABCD0123456789EF")  # 複数台からシリアル番号で選ぶ
device.close()
device = Device.open(path=b"/dev/hidraw0")  # 分かっているデバイスノード
device.close()
```

**Go**

```go
device, err := hidpin.Open("") // つながっている唯一のボード; Open("ABCD0123456789EF") picks one
if err != nil {
	return err
}
defer device.Close()
fmt.Println(device.Serial)
other, err := hidpin.OpenPath("/dev/hidraw0", "") // 分かっているデバイスノード
if err != nil {
	return err
}
other.Close()
```

### 7.2 情報と設定を読む

| 正規名 | 意味 | Python | Go |
|---|---|---|---|
| `info` | デバイス情報。一度読んだら保持する | `info`（プロパティ） | `Info()` |
| `get_pin_config()` | ピン設定のレポートを今読み、その `config` を保持する | `get_pin_config()` | `GetPinConfig()` |
| `pin_config` | 保持している `config`。まだなければ読む | `pin_config`（プロパティ） | `PinConfig()` |

使い方:

**Python**

```python
print(device.info.board_name)  # 一度だけ読み、以後は保持したもの
report = device.get_pin_config()  # 今読む
config = device.pin_config  # 保持している設定
print(config[5])
```

**Go**

```go
info, err := device.Info() // 一度だけ読み、以後は保持したもの
if err != nil {
	return err
}
if _, err := device.GetPinConfig(); err != nil { // 今読む
	return err
}
config, err := device.PinConfig() // 保持している設定
if err != nil {
	return err
}
fmt.Println(info.BoardName(), config[5])
```

### 7.3 ピン設定を書く

`set_pin_config(config)` は、ピン設定の全体を書いて確かめる。手順は次のとおり。

1. `config` を `info.available` に対して `validate_pin_config` で検証する。失敗したら `local` = 真の `PinConfigRejected` で失敗し、何も送らない。
2. 1〜255 から、この `Device` が直前に使ったものと異なる `request_id` を無作為に選ぶ。
3. `encode_pin_config_set` で Set_Report(Feature, ID 3) を送る。
4. ピン設定を読み戻す（Get_Report(Feature, ID 3)）。
5. その `request_id` が異なれば、`PinConfigConflict(expected, seen)` で失敗する。間に別のプロセスが書いた（PROTOCOL-ja.md 6.3）。
6. その `result` が `OK` でなければ、`local` = 偽の `PinConfigRejected(result, result_gpio)` で失敗する。
7. その `config` が `config` と異なれば、一般のエラーで失敗する。
8. 読み戻したレポートを返す。

書き込みが成功し、`info.pull_down_unreliable` が真で `config.pull_down_gpios` が空でないときは、
その言語の普通のやり方で警告すべきである（SHOULD。PROTOCOL-ja.md 5.3、ADR 0004）。
Python は警告 `PullDownUnreliableWarning` を出す。そうした習慣のない言語（Go）のライブラリは呼び出し側に任せ、
呼び出し側は `pull_down_unreliable` と `pull_down_gpios` で判断する。

`update_pins({gpio: setting})` は、ピン設定を今読み、指定したピンを `with_pins` で置き換えて `set_pin_config` を呼ぶ。

| 正規名 | Python | Go |
|---|---|---|
| `set_pin_config(config)` | `set_pin_config(config)` | `SetPinConfig(config)` |
| `update_pins(settings)` | `update_pins(settings)` | `UpdatePins(settings)` |

使い方:

**Python**

```python
from hidpin import PinConfig, PinConfigConflict, PinConfigRejected, PinMode, PinSetting

try:
    device.update_pins({5: PinSetting.monitor(PinMode.PULLUP, 20), 7: PinSetting.output(False)})
except PinConfigRejected as error:
    print(error.result, error.gpio, error.local)  # local: 送る前に見つかった
except PinConfigConflict:
    print("another process wrote at the same time; read again and retry")

device.set_pin_config(PinConfig.default(device.info.available))  # 既定の設定に戻す
```

**Go**

```go
_, err := device.UpdatePins(map[int]hidpin.PinSetting{5: hidpin.MonitorPullUp(20), 7: hidpin.Output(false)})
var rejected *hidpin.PinConfigRejectedError
var conflict *hidpin.PinConfigConflictError
switch {
case errors.As(err, &rejected):
	fmt.Println(rejected.Result, rejected.GPIO, rejected.Local) // Local: 送る前に見つかった
case errors.As(err, &conflict):
	fmt.Println("another process wrote at the same time; read again and retry")
case err != nil:
	return err
}

info, err := device.Info()
if err != nil {
	return err
}
if _, err := device.SetPinConfig(hidpin.DefaultPinConfig(info.Available)); err != nil { // 既定の設定に戻す
	return err
}
```

### 7.4 状態通知

| 正規名 | 意味 | Python | Go |
|---|---|---|---|
| `read_status(timeout)` | Interrupt IN の次の状態通知を待つ。時間切れなら「なし」 | `read_status(timeout_ms)` → `None` | `ReadStatus(timeout)` → `ok` = false |
| `request_status()` | Get_Report(Input) で現在の状態を求める（PROTOCOL-ja.md 4.6） | `request_status()` | `RequestStatus()` |
| `missed_reports` | ホストが読み落とした状態通知の数の累計 | `missed_reports` | `MissedReports()` |

- `read_status` は、ID が 1 でないレポートで失敗しなければならない（MUST）。
- `read_status` は `seq` の飛びを数える。2 通目以降、`(seq - last_seq - 1) mod 65536` を `missed_reports` に加える。
  `reason` に `HOST_REQUEST` がある通知は数えず、`last_seq` も変えない。`request_status` は数を変えない。

使い方:

**Python**

```python
report = device.read_status(timeout_ms=1000)  # 1 秒以内に届かなければ None
if report is not None:
    for event in report.events:
        print(event.gpio, event.level, event.start_us(report.timestamp_us))
now = device.request_status()  # 現在の状態をすぐに得る
print(now.levels, device.missed_reports)
```

**Go**

```go
status, ok, err := device.ReadStatus(time.Second) // 1 秒以内に届かなければ ok は false
if err != nil {
	return err
}
if ok {
	for _, e := range status.Events {
		start, known := e.StartUS(status.TimestampUS)
		fmt.Println(e.GPIO, e.Level, start, known)
	}
}
now, err := device.RequestStatus() // 現在の状態をすぐに得る
if err != nil {
	return err
}
fmt.Println(now.Levels, device.MissedReports())
```

### 7.5 出力

| 正規名 | 意味 | Python | Go |
|---|---|---|---|
| `write_outputs(mask, value)` | 出力指示を 1 つ送る（PROTOCOL-ja.md 7.1） | `write_outputs` | `WriteOutputs` |
| `set_outputs({gpio: high})` | レベルから `mask` と `value` を作って送る | `set_outputs` | `SetOutputs` |

`set_outputs` は、0〜29 の外の GPIO があれば何も送らずに失敗しなければならない（MUST）。
出力ピンかどうかは確かめない。デバイスがそれ以外を無視する（PROTOCOL-ja.md 7.1）。

使い方:

**Python**

```python
from hidpin import PinSetting

device.update_pins({7: PinSetting.output(False), 8: PinSetting.output(False)})
device.set_outputs({7: True, 8: False})  # GPIO7 を HIGH、GPIO8 を LOW に
device.write_outputs(1 << 7, 0)  # マスクで同じことをする: GPIO7 を LOW に
```

**Go**

```go
if _, err := device.UpdatePins(map[int]hidpin.PinSetting{7: hidpin.Output(false), 8: hidpin.Output(false)}); err != nil {
	return err
}
if err := device.SetOutputs(map[int]bool{7: true, 8: false}); err != nil { // GPIO7 を HIGH、GPIO8 を LOW に
	return err
}
if err := device.WriteOutputs(1<<7, 0); err != nil { // マスクで同じことをする: GPIO7 を LOW に
	return err
}
```

### 7.6 極性と ON/OFF

ホストは**ピンレベル**を ON/OFF に読み替える。既定では、プルアップで監視するピンは **LOW = ON**（GND へのスイッチ）で、ほかのピンは HIGH = ON である。

| 正規名 | 意味 | Python | Go |
|---|---|---|---|
| `set_polarity(gpio, active_low)` | 1 本のピンの極性を上書きする | `set_polarity` | `SetPolarity` |
| `is_active_low(gpio, config?)` | 上書きがあればそれ、なければ `config` でのそのピンのモードが `PULLUP` か | `is_active_low` | `IsActiveLow` |
| `on_off(report, config?)` | 通知に含まれる監視ピンすべての `{gpio: on}` | `on_off` | `OnOff` |

`config` の既定値は `pin_config`。

使い方:

**Python**

```python
print(device.is_active_low(5))  # GPIO5 がプルアップなら True
device.set_polarity(5, active_low=False)  # このスイッチは押すと HIGH になる
report = device.request_status()
print(device.on_off(report))  # {0: False, 1: False, ...}
```

**Go**

```go
config, err := device.PinConfig()
if err != nil {
	return err
}
fmt.Println(device.IsActiveLow(5, config)) // GPIO5 がプルアップなら true
device.SetPolarity(5, false)               // このスイッチは押すと HIGH になる
status, err := device.RequestStatus()
if err != nil {
	return err
}
fmt.Println(device.OnOff(status, config)) // map[0:false 1:false ...]
```

## 8. エラー

| 正規名 | 起きるとき | データ | Python | Go |
|---|---|---|---|---|
| （基底） | ライブラリのすべてのエラー | — | `HidpinError` | —（普通の `error`） |
| `DeviceNotFound` | 一致するデバイスがない | — | `DeviceNotFound` | `ErrNotFound` |
| `SeveralDevices` | シリアル番号がなく、デバイスが複数ある | — | `SeveralDevices` | `ErrSeveralDevices` |
| `ProtocolVersionError` | デバイスのプロトコル版が違う | `device_version` | `ProtocolVersionError` | `*ProtocolVersionError` |
| `PinConfigRejected` | 手元またはデバイスでの検証に失敗した | `result`、`gpio`、`local` | `PinConfigRejected` | `*PinConfigRejectedError` |
| `PinConfigConflict` | 間に別のプロセスがピン設定を書いた | `expected`、`seen` | `PinConfigConflict` | `*PinConfigConflictError` |
| `UnsupportedPlatform` | この OS にバックエンドがない | — | —（`SystemBus()` が `HidpinError` を送出） | `ErrUnsupportedPlatform` |
| `DeviceDisconnected` | Watch の操作が、切断中にデバイスを必要とした（9.2） | — | `DeviceDisconnected` | `ErrDisconnected` |
| `WatcherClosed` | 監視役が止まったあとの Watch の操作 | — | `WatcherClosed` | `ErrWatcherClosed` |

- `PinConfigRejected` のメッセージは、`local` なら `the pin configuration is invalid (nothing was sent to the device): <message>`、
  そうでなければ `the device rejected the pin configuration: <message>`。`<message>` は `result_message(result)` で、
  `gpio` が `RESULT_GPIO_NONE` でなければ後ろに ` (GPIOn)` を付ける。
- 呼び出し側は、この表の種類を見分けられなければならない（MUST。Python はクラス、Go は `errors.Is` / `errors.As`）。
  OS や通り道のエラーは、必要なら包んでそのまま渡す。

使い方:

**Python**

```python
from hidpin import Device, DeviceNotFound, HidpinError, ProtocolVersionError, SeveralDevices

try:
    with Device.open() as device:
        print(device.info.board_name)
except SeveralDevices as error:
    print(error)  # それぞれのシリアル番号を挙げる
except DeviceNotFound:
    print("plug in a board")
except ProtocolVersionError as error:
    print("update the firmware or the library:", error.device_version)
except HidpinError as error:
    print(error)  # ライブラリのそのほかのエラー
```

**Go**

```go
device, err := hidpin.Open("")
var version *hidpin.ProtocolVersionError
switch {
case errors.Is(err, hidpin.ErrSeveralDevices):
	fmt.Println(err) // それぞれのシリアル番号を挙げる
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

## 9. Watch（任意の層）

`watch(options)` は常駐プログラム向けの監視役を始め、すぐに戻る。デバイスはまだつながっていなくてよい。
判断の理由は ADR 0002 にある。

イベントは、待ち受けて受け取る形、非同期で受け取る形、またはその両方で提供してよい（MAY）。どの形も、この章のとおりに振る舞う。

| | Python | Go |
|---|---|---|
| 開始 | `watch(serial, target_pins=, active_low=, scan_interval=, bus=)` → `Watcher`。asyncio 向けは `async_watch(...)` → `AsyncWatcher` | `Watch(ctx, WatchOptions)` → `*Watcher` |
| イベントを受け取る | `for event in watcher` または `watcher.get(timeout)`。asyncio では `async for event in watcher` または `await watcher.get()` | `<-w.Events()`（チャネル） |
| 止める | `close()`、`with` / `async with` | `Close()`、または `ctx` の取り消し |
| テスト用の模擬バス | `hidpin.testing.FakeBus`、`FakeBoard` | `hidpintest.Bus`、`Board` |

Python はどちらの形でも、デバイスを専用のスレッドで動かす。`AsyncWatcher` は、イベントループ上でイベントを待つだけである。`scan_interval` の単位は秒。

### 9.1 オプション

| 正規名 | 意味 | 既定値 |
|---|---|---|
| `serial` | 監視するデバイスのシリアル番号 | 空: 最初に見つけた 1 台。以後はそれだけを待つ |
| `target_pins` | **目標ピン設定**。全 GPIO を含み、指定しないものは `UNUSED` | なし: ピン設定を書かず、デバイスの設定に従う |
| `active_low` | 極性の上書き `{gpio: active_low}` | 7.6 のとおり |
| `scan_interval` | 切断中にデバイスを探す間隔 | 1 秒 |
| `bus` | デバイスを探して開くもの。テストでは差し替える | OS |

`watch` は、負の間隔、`active_low` の 0〜29 の外の GPIO、すべての GPIO を利用可能とみなした `validate_pin_config` に通らない `target_pins` で、ただちに失敗しなければならない（MUST）。

### 9.2 操作

| 正規名 | 意味 |
|---|---|
| `events` | イベント（9.3）を起きた順に渡す。監視役が止まるまで続く |
| `error` | 監視役が止まった理由。閉じたか取り消されたときはなし |
| `close()` | 止めてデバイスを閉じる。まだ受け取られていないイベントは捨てる |
| `set_outputs({gpio: high})` | `Device.set_outputs` と同じ。切断中は `DeviceDisconnected` で失敗する。レベルは覚えておかない |
| `set_target_pins(config)` | 目標を置き換える（9.5） |

操作は、イベントを受け取るスレッドを含め、どのスレッドから呼んでも安全でなければならない（MUST）。
アプリがイベントを受け取らない間、監視役はデバイスの読み取りを止める。そのために失われた通知は、次の `StatusReceived` の `missed` に現れる。

使い方:

**Python**

```python
from hidpin import Connected, Disconnected, OnOffChange, PinConfig, PinMode, PinSetting, watch

target = PinConfig.unused().with_pins({5: PinSetting.monitor(PinMode.PULLUP, 20), 7: PinSetting.output(False)})
with watch(target_pins=target) as watcher:  # 複数台あるときは watch("シリアル番号", ...)
    for event in watcher:  # 自前のループなら watcher.get(timeout)
        if isinstance(event, OnOffChange):
            print(f"GPIO{event.gpio}", "ON" if event.on else "OFF", event.time)
        elif isinstance(event, Connected):
            watcher.set_outputs({7: True})
        elif isinstance(event, Disconnected):
            print("unplugged; waiting for the board")
if watcher.error is not None:
    raise watcher.error
```

**Python（asyncio）**

```python
import asyncio

from hidpin import OnOffChange, async_watch


async def main():
    async with async_watch() as watcher:  # デバイス上のピン設定に従う
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
w, err := hidpin.Watch(ctx, hidpin.WatchOptions{TargetPins: &target}) // 複数台あるときは Serial で選ぶ
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

### 9.3 イベント

| イベント | フィールド | 意味 |
|---|---|---|
| `Connected` | `serial`、`path`、`info`、`pin_config`、`reconnected` | デバイスを開き、目標があればそれに合わせた。`pin_config` は以後の設定。`reconnected` は初回だけ偽 |
| `Disconnected` | `error` | 接続が失われた。監視役は同じデバイスを探し続ける |
| `ConnectFailed` | `error` | デバイスはあるが使えない（権限、プロトコル版など）。同じ原因が 2 回続いたときに、原因ごとに 1 回だけ通知する。監視役は試し続ける |
| `StatusReceived` | `status`、`missed`、`on` | 受け取った状態通知そのもの。監視役が自分で求めたものも含む。`missed` はその直前に失われた通知の数、`on` はその通知の `on_off` |
| `InitialOnOff` | `on` | 監視役がそれまで知らなかった監視ピンの ON/OFF。初回接続後の全ピンと、監視を始めたピンやプルを変えたピン。変化ではない |
| `OnOffChange` | `gpio`、`on`、`level`、`has_time`、`time`、`device_us`、`inferred` | 監視ピンの ON/OFF が変わった（9.6） |
| `OutputsReset` | `gpios`、`cause` | これらの出力ピンが今**初期出力レベル**にある。`cause` は `SUSPEND`（通知に `OUTPUT_RESET` があった）、`RECONNECT`、`PIN_CONFIG`（書き込みで出力ピンになったか、初期出力レベルが変わった） |
| `PinConfigChanged` | `config` | デバイスのピン設定が変わった。目標がないときだけ |
| `PinConfigRestored` | `found`、`count` | 別のプログラムがピン設定を変え、監視役が目標を書き戻した。`count` はそのたびに増え、増え続けるなら取り合いが起きている |

### 9.4 接続

デバイスを見つけると、監視役は次のことをする。

1. 開き、デバイス情報（プロトコル版の確認）とピン設定を読む。
2. 目標があって異なれば、それを書く（`set_pin_config`）。ここでの `PinConfigRejected` は監視役をそのエラーで止める。それ以外の失敗は、試行の失敗として扱う。
3. `Connected` を出し、続けて、再接続なら全出力ピンについて `RECONNECT` の、そうでなければ手順 2 で駆動したピンについて `PIN_CONFIG` の `OutputsReset` を出す。
4. `request_status` で現在の状態を求め、**基準**とする。手順 2 でピン設定を書いた場合は、まだ基準を取らない。
   続いて届く `CONFIG_CHANGED` の通知が新しい状態を運ぶので、代わりにそれを使う。1 秒以内に届かなかったときだけ基準を取る。

監視役がエラーで止まるのは、アプリが不可能なことを求めたときだけである。ボードが拒む目標、`serial` がなく複数のデバイス、この OS にバックエンドがない、の 3 つ。
それ以外（権限、抜き差し、プロトコル版の違い）は通知して、`scan_interval` ごとに試し直す。

### 9.5 目標を保つ

- `CONFIG_CHANGED` の通知を受けると、監視役はピン設定を読む。目標がなければ、最後に見たものと異なるとき `PinConfigChanged` を出す。
  目標があって異なれば、目標を書き戻して `PinConfigRestored` を出す。書き戻しは 1 秒に 1 回までで、`PinConfigConflict` のあとは 1 秒後に試し直す。
- `set_target_pins` は、デバイスに一度つないだあとなら `available` に対して検証し、失敗すれば `PinConfigRejected` を返して元の目標を保つ。
  接続中ならすぐ書き、それがほかの理由で失敗しても新しい目標は残り、あとで書く。切断中なら次の接続で書く。
- ピンを出力にしたり初期出力レベルを変えたりする書き込みは、どれも `PIN_CONFIG` の `OutputsReset` を出す。
  監視役は、出力を以前のレベルに駆動し直すことはしない（ADR 0002）。

### 9.6 ON/OFF を追う

監視役は、各監視ピンの ON/OFF を接続をまたいで保持し、各状態通知から変化を導く。各変化はちょうど 1 回だけ通知する。

- 基準より前に送られた通知は、基準にすでに含まれている。`seq` が基準の `seq` より後でない（65536 を法として比べる）割り込みの通知は、
  `StatusReceived` としては渡すが、変化の導出には使わない。`MORE_EVENTS` のある基準や、`seq` = 0 の基準は基準にしない。
- ピン設定を書いたあとは、`CONFIG_CHANGED` の通知が届くまで（または 1 秒たって基準を取り直すまで）通知を使わない。
  それより前の通知は古い設定を示すからである（PROTOCOL-ja.md 6.4）。
- 知っているピンのエッジイベントごとに、時刻つきの `OnOffChange` を出す。そのエッジイベントがピンを元と同じ状態にするなら、反対向きの変化が失われているので、先にそれを `inferred` = 真で出す。
- 続いて各監視ピンについて、知らなかったピンは `InitialOnOff` に入れ、知っている状態とレベルが食い違うピンには `inferred` = 真で時刻なしの `OnOffChange` を出す（エッジイベントが失われたか、切断中に変化した）。
- 監視をやめたピンやプルが変わったピンは忘れ、改めて知る。

**変化の時刻。** `device_us` は、デバイスの時計での変化の始まり（`start_us`）である。`time` は同じ瞬間のホストの時刻である。
監視役は通知ごとに、受け取ったホストの時刻とその `timestamp_us` を組にし、現在と直前の 30 秒の中で「ホストの時刻 − デバイスの時刻」が最小の組を保つ。
デバイスの時刻はその組を通して変換する。飽和した変化と補った変化には時刻がない（`has_time` = 偽）。

## 10. ホストライブラリのテスト

- `protocol/vectors.json` のすべての事例が往復しなければならない（MUST）。復号するとファイルのフィールドになり、そのフィールドを符号化するとファイルのペイロードになる。
  同じファイルを C のコアも検査するので、合格すればファームウェアとバイト単位で一致する。
- `validate_pin_config_payload` は、すべての `pin_config_set` の事例で `result`、`result_gpio`、`request_id` を出さなければならない（MUST）。
- ライブラリは、PROTOCOL-ja.md に従う偽のデバイスをテスト用に添えるべきである（SHOULD。Python はモジュール `hidpin.testing`、Go はパッケージ `hidpintest` で、どちらも抜き差しを再現する）。
  そうすれば、アプリを実機なしでテストできる。

使い方:

**Python**

```python
from hidpin import Device, InitialOnOff, OnOffChange, watch
from hidpin.testing import FakeBoard, FakeBus

board = FakeBoard("ABCD0123456789EF")
bus = FakeBus(board)
with Device(bus.open(bus.devices()[0])) as device:  # 模擬ボード上の Device
    print(device.info.board_name)
with watch(bus=bus, scan_interval=0.01) as watcher:
    next(e for e in watcher if isinstance(e, InitialOnOff))
    board.set_input(5, False)  # GPIO5 のスイッチを押す
    change = next(e for e in watcher if isinstance(e, OnOffChange))
    assert change.gpio == 5 and change.on
    bus.unplug(board)  # bus.plug(board) で戻す
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
board.SetInput(5, false) // GPIO5 のスイッチを押す
bus.Unplug(board)        // bus.Plug(board) で戻す

device := hidpin.NewDevice(hidpintest.New(hidpin.ProtocolVersion), "ABCD0123456789EF") // 偽のデバイス上の Device
defer device.Close()
```

## 11. 既存ライブラリの状況

どちらのライブラリもこの文書に従い、両方の層を提供する。Python 版は Watch の層を、待ち受ける形と asyncio の形の両方で提供する。Go 版はチャネルで提供する。
