# hidpin

[English](README.md) | **日本語**

RP2040 マイコンボードの GPIO を監視し、その状態を USB-HID でコンピュータに伝える仕組み。
ボードは**ベンダー定義の HID デバイス**として認識されるので、OS 標準のドライバだけで動く。
キーボードやマウスのふりはしないため、ホスト側のアプリから全ピンの状態をそのまま読める。

対応ボード（for RP2040 boards）:

| ボード | 利用可能GPIO | 状態 LED |
|---|---|---|
| Raspberry Pi Pico | 26 本（GPIO0–22, 26–28） | 本体の LED（GPIO25）：点灯、入出力時に一瞬消える |
| Adafruit QT Py RP2040 | 13 本（GPIO3–6, 20, 22–29） | NeoPixel（GPIO12）：緑、入出力時に一瞬青 |

状態 LED は電源が入るとすぐに点く。「入出力」は、監視ピンのエッジイベントと出力指示の適用のこと。
そのたびに約 100 ms だけ表示が変わる。

> **現在の状態**: PC 上のテスト（ファームウェアのコア 33 件、ホスト 60 件）に加えて、
> **Adafruit QT Py RP2040 の実機で動作を確認済み**（2026-09-17）。
> 認識、監視とエッジイベント、チャタリング除去、ピン設定、出力、バスリセット時の復帰まで確認した。
> Raspberry Pi Pico は未確認。手順は [docs/TESTING.md](docs/TESTING.md) にある。

## できること

- 監視ピンの**ピンレベル**（HIGH/LOW）を、変化したとき・1 秒ごと・ホストが要求したときに受け取る
- **エッジイベント**（いつ変化したか）を μs の時刻つきで受け取る。1 通に最大 7 個
- ピンごとの設定：監視するか、プルアップ／プルダウン、チャタリング除去時間（0–255 ms）、出力
- 出力ピンの駆動。USB の切断・サスペンド時には安全側（初期出力レベル）に戻る
- 複数台の同時接続。USB シリアル番号（ボード固有 ID）で区別する

できないこと: アナログ入力（ADC）、I2C や SPI のブリッジ、キーボードとしての入力。

## 使い方

### 1. ファームウェアを書き込む

必要なもの: CMake 3.20 以上、Ninja、`arm-none-eabi-gcc`。
Pico SDK は `PICO_SDK_PATH` があればそれを使い、なければ 2.3.1 を自動で取得する。

```
cmake -S firmware -B build/pico -G Ninja -DPICO_BOARD=pico   # QT Py は adafruit_qtpy_rp2040
cmake --build build/pico
```

BOOTSEL ボタンを押しながら USB をつなぎ、現れたドライブに `build/pico/hidpin.uf2` をコピーする。

デバッグ用に USB シリアル（CDC）でログを出すビルドは `-DHIDPIN_DEBUG=ON` を付ける。

### 2. ホスト側を用意する（Linux）

```
sudo cp udev/70-hidpin.rules /etc/udev/rules.d/
sudo udevadm control --reload-rules && sudo udevadm trigger
```

ルールを入れたら、**ボードを挿し直す**。挿したままだと権限が変わらない。

コマンドとして使うなら、次のどれかで入れる。

```
uv tool install ./host      # hidpin コマンドがどこからでも使える
pipx install ./host         # 同上
pip install ./host          # 今の Python 環境に入れる
```

インストールせずに試すなら `cd host && uv run hidpin list` のように実行する。

同じコマンドの Go 版と、Go 用のドライバライブラリが [`host-go`](host-go/README.md) にある
（`go install github.com/yukkeorg/hidpin/host-go/cmd/hidpin@latest`、Linux 専用）。

Linux では `/dev/hidraw*` を直接使うので、追加のライブラリは要らない。
Windows と macOS では hidapi が必要になる（`pip install './host[hidapi]'`）。
`HIDPIN_BACKEND=hidraw` または `HIDPIN_BACKEND=hidapi` で明示的に選べる。
hidapi の PyPI ホイールは libusb 版で、カーネルドライバを奪えないと
`OSError: open failed` になることがあるため、Linux では hidraw を既定にしている。

Python 3.11 以上。動作確認は Linux（hidraw）で行っている。Windows と macOS は hidapi 経由で動く見込みだが、未確認。

### 3. コマンドを使う

```
hidpin list                             # つながっているデバイス（シリアル番号つき）
hidpin info                             # ボード、ファームウェア版、利用可能GPIO
hidpin watch                            # 状態通知を表示し続ける
hidpin config get                       # 現在のピン設定
hidpin config set 5=pullup:20 6=off     # 指定したピンだけ変更
hidpin config set 7=out:low             # 出力ピンにする（初期出力レベルは LOW）
hidpin output 7=high                    # 出力の値を変える
```

どのコマンドも `--json` で機械可読な出力になり、複数台つながっているときは `--serial` で選ぶ。

スイッチは GND につなぎ、プルアップで使う想定。押すと LOW になるので、ホスト側は既定で
「LOW = ON」と解釈する。`hidpin watch --active-high 7` のように、ピンごとに変更できる。

### 4. ライブラリとして使う

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
host/                 Python ライブラリと CLI
host-go/              Go のライブラリと CLI（Linux 専用、cgo 不要）。host-go/README.md を参照
udev/                 Linux の udev ルール
```

プロトコルを変えるときは、`docs/PROTOCOL.md` を直し、`protocol/vectors.json` を更新し、
C と Python の両方の実装を合わせる。仕様書が正で、テストデータはそこから導く。

## 開発

```
# ファームウェアのコアのテスト（Pico SDK は不要）
cmake -S firmware/test -B build/core-tests -G Ninja
cmake --build build/core-tests && ctest --test-dir build/core-tests

# ホスト側のテスト
cd host && uv run --group dev pytest
```

## USB の識別番号について

既定値は pid.codes のテスト用 PID **1209:0001**。これは**社内でのテスト専用**で、
再配布・販売・製造する機器には使えない。配布するなら [pid.codes](https://pid.codes/howto/) で
正式な PID を取得する（公開リポジトリと OSS ライセンスがあれば無償）。

取得した番号でのビルドと接続:

```
cmake -S firmware -B build/pico -G Ninja -DPICO_BOARD=pico -DHIDPIN_USB_PID=0x1234
cmake --build build/pico

HIDPIN_PID=0x1234 hidpin list        # ホスト側は環境変数で合わせる
```

`HIDPIN_USB_VID` / `HIDPIN_USB_PID` がファームウェア側、`HIDPIN_VID` / `HIDPIN_PID` が
ホスト側の指定。udev ルール（`udev/70-hidpin.rules`）の `idProduct` は手で書き換える。

## ライセンス

MIT License（[LICENSE](LICENSE)）。

含まれるサードパーティのコード:

- `firmware/app/ws2812.pio` — pico-examples より。Copyright (c) 2020 Raspberry Pi (Trading) Ltd.、BSD-3-Clause
- `firmware/pico_sdk_import.cmake` — Pico SDK より。Copyright (c) 2020 Raspberry Pi (Trading) Ltd.、BSD-3-Clause

ビルド時に取得する Pico SDK（BSD-3-Clause）、TinyUSB（MIT）、実行時に使う hidapi
（BSD-3-Clause / GPL-3.0 / 独自ライセンスから選択）は、それぞれの配布元のライセンスに従う。
