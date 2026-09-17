# 実機での確認手順

PC 上のユニットテスト（`firmware/test`、`host/tests`）では確かめられない部分を、実機で確認する手順。
用語は [CONTEXT.md](../CONTEXT.md)、レポートの内容は [PROTOCOL.md](./PROTOCOL.md) を参照。

## 用意するもの

- Raspberry Pi Pico または Adafruit QT Py RP2040
- USB ケーブル（データ線のあるもの。充電専用ケーブルでは認識されない）
- タクトスイッチ 1 個とジャンパ線（GPIO5 と GND の間につなぐ）
- LED 1 個と抵抗 330Ω 程度（出力の確認用。GPIO7 → 抵抗 → LED → GND）
- ホスト側の準備

  ```
  sudo cp udev/70-hidpin.rules /etc/udev/rules.d/
  sudo udevadm control --reload-rules && sudo udevadm trigger
  uv tool install ./host        # インストールせずに試すなら cd host && uv run hidpin ...
  ```

  ルールを入れた後にボードを挿し直す。挿したままだと権限が変わらず、
  `OSError: open failed` のままになる。Linux では hidraw を直接使うので、
  hidapi のインストールは不要。

配線は 3.3V 系。**RP2040 は 5V トレラントではない**ので、外部機器の信号を直接つながない。

## 1. 書き込み

1. ファームウェアをビルドする。

   ```
   cmake -S firmware -B build/pico -G Ninja -DPICO_BOARD=pico   # QT Py は adafruit_qtpy_rp2040
   cmake --build build/pico
   ```
2. ボードの BOOTSEL ボタン（QT Py は BOOT ボタン）を押しながら USB をつなぐ。
3. `RPI-RP2` という USB メモリが現れるので、`build/pico/hidpin.uf2` をコピーする。

- [ ] コピー後にドライブが自動的に消え、ボードが再起動する

## 2. USB で認識されること

```
lsusb | grep 1209:0001
hidpin list
hidpin info
```

- [ ] `lsusb` に `1209:0001` が出る
- [ ] `hidpin list` にシリアル番号（16 桁の 16 進数）が出る
- [ ] `hidpin info` のボード名と利用可能GPIOが、つないだボードと一致する（Pico は 26 本、QT Py は 13 本）
- [ ] 状態 LED が点灯する（Pico は本体の LED、QT Py は NeoPixel が緑）
- [ ] `dmesg` に `hidraw` として現れ、キーボードやマウスとしては認識されない

USB ケーブルを抜くと LED が消える（電源も切れる）。

## 3. 状態通知とスイッチ入力

GPIO5 と GND の間にスイッチをつなぐ。既定ピン設定はプルアップなので、押すと LOW になる。

```
hidpin watch
```

- [ ] 最初の行に全監視ピンの ON/OFF が出て、GPIO5 は `OFF`（押していないので HIGH）
- [ ] スイッチを押すと `GPIO5  ON  (LOW)` が 1 行出る
- [ ] 離すと `GPIO5  OFF (HIGH)` が 1 行出る
- [ ] 1 回の押し離しで、余分な行が出ない（チャタリングが除去されている）
- [ ] 表示される時刻の差が、実際に押していた時間とおおよそ一致する

`hidpin --json watch` では 1 行 1 レポートの JSON が出る。`events` の `start_us` が変化の開始時刻。

- [ ] 押しっぱなしにしても、1 秒ごとの定期通知（`reason` に `PERIODIC`）は出続ける

## 4. チャタリング除去時間の効果

```
hidpin config set 5=pullup:0     # 除去なし
hidpin watch
```

- [ ] 1 回押しただけで複数の行が出ることがある（接点のバタつきがそのまま見える）

```
hidpin config set 5=pullup:20    # 既定値に戻す
```

- [ ] 1 回の押し離しが 1 行ずつに戻る

## 5. ピン設定の読み書き

```
hidpin config get
hidpin config set 6=off
hidpin config get
hidpin config set 23=pullup:20   # Pico では利用可能GPIOではない
```

- [ ] `config set 6=off` の後、`config get` の一覧から GPIO6 が消える
- [ ] `watch` に `ピン設定が変更されました` が出る（`reason` に `CONFIG_CHANGED`）
- [ ] 利用可能GPIOでないピンを指定すると、エラーになり設定は変わらない
- [ ] USB を抜き差しすると、ピン設定が既定（全ピン `pullup:20`）に戻る

## 6. 出力

GPIO7 に LED をつなぐ。

```
hidpin config set 7=out:low
hidpin output 7=high
hidpin output 7=low
```

- [ ] `out:low` にした時点では LED は消えている
- [ ] `output 7=high` で LED が点く
- [ ] `output 7=low` で消える
- [ ] `hidpin watch` に出力の変化が出る（`reason` に `OUTPUT_APPLIED`）
- [ ] 出力ピンではない GPIO を指定すると警告が出て、何も変わらない

USB サスペンドの確認（PC をスリープさせるか、`/sys/bus/usb/devices/.../power/control` を使う）。

- [ ] LED を点けた状態でサスペンドすると、**初期出力レベル（LOW）に戻って消える**
- [ ] 復帰後の `watch` に `USB の切断かサスペンドにより…` が出る（`reason` に `OUTPUT_RESET`）

`OUTPUT_RESET` は再接続直後の最初の状態通知に立つ。`watch` を止めてから接続し直した場合は
受け取れない（出力が初期出力レベルに戻っていることは `levels` で確認できる）。
USB バスリセットでも同じ動作になる。root なしで試すには、hidraw に対応する USB ノードに
`USBDEVFS_RESET`（`_IO('U', 20)` = `0x5514`）を ioctl で送る。

## 7. 取りこぼしと欠落の検出

```
hidpin --json watch > /tmp/hidpin.jsonl
```

スイッチを何度か操作してから停止し、記録を確認する。

- [ ] `seq` が 1 ずつ増えている（`missed_reports` が 0 のまま）
- [ ] `flags` に `OVERFLOW` が出ない

スイッチの端子をこすり合わせるなどして、短時間に多数の変化を起こす。

- [ ] `flags` に `OVERFLOW` が出た場合でも、その後の `levels` は実際のピンの状態と一致する

## 8. 複数台の同時接続（2 台ある場合）

- [ ] `hidpin list` に 2 つのシリアル番号が出る
- [ ] `--serial` なしで `hidpin info` を実行すると、どちらを使うか指定するよう促される
- [ ] `--serial` で指定すると、そのデバイスだけを操作できる

## 9. デバッグ版

```
cmake -S firmware -B build/pico-debug -G Ninja -DPICO_BOARD=pico -DHIDPIN_DEBUG=ON
cmake --build build/pico-debug
```

書き込み後、

- [ ] `hidpin list` は変わらず動く（HID の見え方は同じ）
- [ ] `/dev/ttyACM0` が現れ、`hidpin config set` などの操作時にログが出る

  ```
  screen /dev/ttyACM0 115200     # または: cat /dev/ttyACM0
  ```

## 10. 他の OS（任意）

Windows と macOS は「対応」とはしていないが、動かす場合の確認。

- [ ] ドライバの追加インストールなしで認識される
- [ ] `hidpin list` と `hidpin watch` が動く
