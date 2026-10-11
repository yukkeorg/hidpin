# hidpin

[English](README.md) | **日本語**

hidpin は、RP2040 / RP2350 マイコンボードを、GPIO を監視してその状態を USB-HID でコンピュータに伝えるデバイスにします。
ボードは**ベンダー定義の HID デバイス**として認識されるので、OS 標準のドライバだけで動きます。
キーボードやマウスのふりはしません。ホスト側では同梱のライブラリを使うことで、アプリから全ピンの状態をそのまま読めます。

プリセットのあるボード:

| ボード                | 利用可能GPIO                | 状態 LED                                         |
| --------------------- | --------------------------- | ------------------------------------------------ |
| Raspberry Pi Pico     | 26 本（GPIO0–22, 26–28）    | 本体の LED（GPIO25）：点灯、入出力時に一瞬消える |
| Adafruit QT Py RP2040 | 13 本（GPIO3–6, 20, 22–29） | NeoPixel（GPIO12）：緑、入出力時に一瞬青         |
| Raspberry Pi Pico 2   | 26 本（GPIO0–22, 26–28）    | 本体の LED（GPIO25）：点灯、入出力時に一瞬消える |

Pico SDK が知っているほかの RP2040 / RP2350A のボードも、ビルド時に利用可能GPIOを指定すれば使えます（[ファームウェアをビルドする](#1-ファームウェアをビルドする)を参照）。
なお、現在は RP2350B（48 本）のボードには対応していません。

状態 LED は電源が入るとすぐに点きます。

> [!WARNING]
> RP2350 の初期の版（A2）では、内蔵プルダウンで入力を LOW に保てません（エラッタ E9）。
> ボードがこのことを申告し、`hidpin info` に表示されるほか、プルダウンで監視するピンを設定するとホストのツールが警告します。
> プルアップを使うか、8.2 kΩ 以下の外付けのプルダウンと `nopull` を組み合わせてください。既定の設定はプルアップなので影響はありません。

## できること

- 監視ピンの**ピンレベル**（HIGH/LOW）を、変化したとき・1 秒ごと・ホストが要求したときに受け取る
- **エッジイベント**（いつ変化したか）を μs の時刻つきで受け取る。1 通に最大 7 個
- ピンごとの設定：監視するか、プルアップ／プルダウン、チャタリング除去時間（0–255 ms）、出力
- 出力ピンの駆動。USB の切断・サスペンド時には安全側（初期出力レベル）に戻る
- 複数台の同時接続。USB シリアル番号（ボード固有 ID）で区別する

GPIO として使えるピンを基本的にすべて hidpin が使うため、アナログ入力（ADC）、I2C や SPI のブリッジ、キーボードとしての入力はできません。

## 使い方

### 1. ファームウェアをビルドする

必要なものは、CMake 3.20 以上、Ninja、`arm-none-eabi-gcc` です。
Pico SDK は、`PICO_SDK_PATH` があればそれを使い、なければ 2.3.1 を自動で取得します。

```
cmake -S firmware -B build/pico -G Ninja -DPICO_BOARD=pico   # Pico 2 は pico2、QT Py は adafruit_qtpy_rp2040
cmake --build build/pico
```

プリセットのないボードでは、利用可能GPIOを列挙してください。ボード上の部品につながっている GPIO は外します。
たとえば Pico W では、GPIO23〜25 と 29 が無線チップにつながっています。

```
cmake -S firmware -B build/pico_w -G Ninja -DPICO_BOARD=pico_w -DHIDPIN_GPIOS=0-22,26-28
```

このようなボードで `HIDPIN_GPIOS` がないとき、またはボードが RP2350B のときは、構成の時点でエラーになります。
`HIDPIN_GPIOS` は、上の表のボードのプリセットを置き換えるのにも使えます。
プリセットのないボードは、ボード種別 0 と Pico SDK のボード名を申告します（`hidpin info` に表示されます）。

デバッグ用に USB シリアル（CDC）でログを出すビルドにするには、`-DHIDPIN_DEBUG=ON` を付けます。

状態 LED はボード自身のものを使います。別の GPIO に付けた LED を使う場合は、構成するときに指定します。

```
-DHIDPIN_STATUS_LED_PIN=15                               # 普通の LED（HIGH で点灯）
-DHIDPIN_STATUS_LED_PIN=16 -DHIDPIN_STATUS_LED_TYPE=ws2812  # WS2812（NeoPixel）
```

指定した GPIO はプリセットから外れ、監視にも出力にも使えなくなります。
`HIDPIN_GPIOS` で一覧を指定するときは、状態 LED の GPIO を含めないでください。

### 2. ファームウェアを書き込む

BOOTSEL ボタン（QT Py は BOOT ボタン）を押しながら USB をつなぎ、現れたドライブにビルドディレクトリの `hidpin.uf2`（上の例では `build/pico/hidpin.uf2`）をコピーします。
ドライブは自動的に消え、ボードが hidpin として再起動します。

### 3. ホスト側を用意する（Linux）

```
sudo cp udev/60-hidpin.rules /etc/udev/rules.d/
sudo udevadm control --reload-rules && sudo udevadm trigger
```

ルールを入れたら、**ボードを挿し直してください**。挿したままでは権限が変わりません。

コマンドとして使う場合は、次のどれかでインストールします。

```
uv tool install ./python      # hidpin コマンドがどこからでも使える
pipx install ./python         # 同上
pip install ./python          # 今の Python 環境に入れる
```

インストールせずに試す場合は、`cd python && uv run hidpin list` のように実行します。

同じコマンドの Go 版と、Go 用のホストライブラリが [`hidpin/`](hidpin/README.md) にあります
（`go install github.com/yukkeorg/hidpin/cmd/hidpin@latest`、`import "github.com/yukkeorg/hidpin/hidpin"`、
Linux 専用）。

Linux では `/dev/hidraw*` を直接使うので、追加のライブラリは要りません。
Windows と macOS では hidapi が必要です（`pip install './python[hidapi]'`）。
`HIDPIN_BACKEND=hidraw` または `HIDPIN_BACKEND=hidapi` で明示的に選ぶこともできます。
hidapi の PyPI ホイールは libusb 版で、カーネルドライバを奪えないと
`OSError: open failed` になることがあるため、Linux では hidraw を既定にしています。

Python 3.11 以上が必要です。動作確認は Linux（hidraw）で行っています。Windows と macOS は hidapi 経由で動く見込みですが、まだ確認していません。

### 4. コマンドを使う

```
hidpin list                             # つながっているデバイス（シリアル番号つき）
hidpin info                             # ボード、ファームウェア版、利用可能GPIO
hidpin watch                            # 状態通知を表示し続ける
hidpin config get                       # 現在のピン設定
hidpin config set 5=pullup:20 6=off     # 指定したピンだけ変更
hidpin config set 7=out:low             # 出力ピンにする（初期出力レベルは LOW）
hidpin output 7=high                    # 出力の値を変える
```

どのコマンドも `--json` を付けると機械可読な出力になります。複数台つながっているときは `--serial` で選びます。

スイッチは GND につなぎ、プルアップで使う想定です。押すと LOW になるので、ホスト側は既定で
「LOW = ON」と解釈します。`hidpin watch --active-high 7` のように、ピンごとに変更できます。

### 5. ライブラリとして使う

```python
from hidpin import Device, PinMode, PinSetting

with Device.open() as device:                      # 複数台あるときは Device.open("シリアル番号")
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

## リポジトリの構成

```
CONTEXT.md            用語集（デバイス、監視ピン、ピンレベル、状態通知、エッジイベント…）
docs/CONCEPT.md       最初の構想
docs/PROTOCOL.md      USB-HID プロトコル版 1 の仕様（正）
docs/TESTING.md       実機での確認手順
docs/adr/             設計判断の記録
protocol/vectors.json C と Python の両方のテストが参照するテストデータ
firmware/core/        SDK に依存しないコア（レポート、チャタリング除去、イベント、エンジン）
firmware/app/         Pico SDK + TinyUSB のファームウェア
firmware/test/        コアのユニットテスト（PC 上で実行）
python/               Python のホストライブラリと CLI（パッケージ hidpin）
hidpin/               Go のホストライブラリ（パッケージ hidpin、Linux 専用、cgo 不要）。hidpin/README.md を参照
cmd/hidpin/           Go 版の CLI
go.mod                Go のモジュール github.com/yukkeorg/hidpin
udev/                 Linux の udev ルール
```

`docs/` の文書は英語が基本で、日本語訳は同じ場所の `*-ja.md`
（例: [docs/PROTOCOL-ja.md](docs/PROTOCOL-ja.md)）にあります。用語集の `CONTEXT.md` は日本語です。

プロトコルを変えるときは、`docs/PROTOCOL.md`（英語）とその日本語訳 `docs/PROTOCOL-ja.md` を直し、`protocol/vectors.json` を更新して、
C と Python の両方の実装を合わせます。仕様書が正で、テストデータはそこから導きます。

## 開発

```
# ファームウェアのコアのテスト（Pico SDK は不要）
cmake -S firmware/test -B build/core-tests -G Ninja
cmake --build build/core-tests && ctest --test-dir build/core-tests

# ホスト側のテスト
cd python && uv run --group dev pytest    # Python
go test ./...                             # Go（リポジトリ直下で）
```

## USB の識別番号について

hidpin には、[pid.codes](https://pid.codes/1209/6870/) から割り当てられた専用の番号
**1209:6870** があります。ファームウェア、ホストのツール、udev ルールは、既定でこの番号を使います。

ファームウェアを改造した機器を配布する場合は、別の PID を取得してください。

別の番号でのビルドと接続:

```
cmake -S firmware -B build/pico -G Ninja -DPICO_BOARD=pico -DHIDPIN_USB_PID=0x0001
cmake --build build/pico

HIDPIN_PID=0x0001 hidpin list        # ホスト側は環境変数で合わせる
```

`HIDPIN_USB_VID` / `HIDPIN_USB_PID` はファームウェア側、`HIDPIN_VID` / `HIDPIN_PID` は
ホスト側の指定です。udev ルール（`udev/60-hidpin.rules`）の `idProduct` は手で書き換えてください。

ビルドディレクトリは、構成したときの PID を覚えています。1209:6870 への切り替え前に構成したものは
1209:0001 のままビルドされるので、`-DHIDPIN_USB_PID=0x6870` を付けて構成し直すか、削除してください。

## ライセンス

MIT License（[LICENSE](LICENSE)）です。

含まれるサードパーティのコード:

- `firmware/app/ws2812.pio` — pico-examples より。Copyright (c) 2020 Raspberry Pi (Trading) Ltd.、BSD-3-Clause
- `firmware/pico_sdk_import.cmake` — Pico SDK より。Copyright (c) 2020 Raspberry Pi (Trading) Ltd.、BSD-3-Clause

ビルド時に取得する Pico SDK（BSD-3-Clause）、TinyUSB（MIT）、実行時に使う hidapi
（BSD-3-Clause / GPL-3.0 / 独自ライセンスから選択）は、それぞれの配布元のライセンスに従います。
