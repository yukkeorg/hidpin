# pid.codes への申請用の下書き

USB の VID `0x1209`（pid.codes）配下に、このプロジェクト専用の PID を登録するための下書き。
申請の条件と手順は [pid.codes の How to](https://pid.codes/howto/) を参照。

> **状況**: 2026-09-28 に受理された（[PR #1280](https://github.com/pidcodes/pidcodes.github.com/pull/1280)）。
> 登録内容は <https://pid.codes/1209/6870/>。2026-10-04 に既定値を `1209:6870` へ切り替えた。

## 申請するもの

| ファイル | 置き場所（フォーク先のリポジトリ内） |
|---|---|
| `org/yukke.org/index.md` | 組織ページ |
| `1209/6870/index.md` | 機器ページ（PID = `0x6870`、ASCII で "hp"） |

`0x6870` は 2026-09-17 時点で、登録済み 921 件とオープンな PR 85 件のどちらとも衝突していない。
申請前にもう一度、[1209 の一覧](https://github.com/pidcodes/pidcodes.github.com/tree/master/1209)と
オープンな PR を確認する。

## 前提条件（達成済み）

pid.codes は「公開されたソース」と「OSS ライセンス」を条件にしている。
このリポジトリは <https://github.com/yukkeorg/hidpin> で公開済みで、MIT ライセンスの
`LICENSE` がある。下書きの `site` と `source` はこの URL を指している。

このプロジェクトをフォークして別の PID を取る場合は、`owner`・`site`・`source` を
自分のものに書き換えること。

## 申請の手順

1. [pidcodes/pidcodes.github.com](https://github.com/pidcodes/pidcodes.github.com) をフォークする
2. このディレクトリの `org/` と `1209/` の中身を、フォークした作業ツリーの同じ場所にコピーする
3. コミットしてプルリクエストを送る（1 機器につき 1 PID。複数欲しい場合は理由の説明が要る）

## 受理されたあとにやったこと（2026-10-04）

`1209:0001`（テスト用 PID）から、割り当てられた `1209:6870` に既定値を切り替えた。書き換えたのは次の箇所。

- `firmware/CMakeLists.txt` の `HIDPIN_USB_PID` の既定値と、`firmware/app/usb_descriptors.c` の `HIDPIN_USB_PID`
- `host/src/hidpin/device.py` の `DEFAULT_PRODUCT_ID` と、そのテスト
- `host-go/hidpin/device.go` の `DefaultProductID` と、そのテスト
- `udev/60-hidpin.rules` の `idProduct`（2 行）
- `docs/PROTOCOL.md` §1、`docs/TESTING.md`、`README.md`、`README.ja.md` の表記
- `rust-firmware` ブランチの `firmware-rs/app/build.rs` の既定値と `firmware-rs/README.md`

切り替え後は、ファームウェアを書き込み直し、udev ルールを入れ直してボードを挿し直す。

CMake のビルドディレクトリは、構成したときの `HIDPIN_USB_PID` をキャッシュに覚えている。
切り替え前に構成したディレクトリは `0x0001` のままビルドされるので、構成し直す。

```
cmake -S firmware -B build/pico -DHIDPIN_USB_PID=0x6870
cmake --build build/pico        # 書き込み直す
```
