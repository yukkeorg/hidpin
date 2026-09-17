# pid.codes への申請用の下書き

USB の VID `0x1209`（pid.codes）配下に、このプロジェクト専用の PID を登録するための下書き。
申請の条件と手順は [pid.codes の How to](https://pid.codes/howto/) を参照。

## 申請するもの

| ファイル | 置き場所（フォーク先のリポジトリ内） |
|---|---|
| `org/yukke.org/index.md` | 組織ページ |
| `1209/6870/index.md` | 機器ページ（PID = `0x6870`、ASCII で "hp"） |

`0x6870` は 2026-09-17 時点で、登録済み 921 件とオープンな PR 85 件のどちらとも衝突していない。
申請前にもう一度、[1209 の一覧](https://github.com/pidcodes/pidcodes.github.com/tree/master/1209)と
オープンな PR を確認する。

## 申請の前にやること

1. **リポジトリを公開する。** pid.codes は公開されたソースと OSS ライセンスを条件にしている。
   このリポジトリは MIT ライセンスで `LICENSE` があるので、公開すれば条件を満たす。
2. 下書きの `GITHUB_USER` を実際の GitHub ユーザー名に置き換える。

   ```
   grep -rl GITHUB_USER docs/pid-codes | xargs sed -i 's/GITHUB_USER/<ユーザー名>/g'
   ```

## 申請の手順

1. [pidcodes/pidcodes.github.com](https://github.com/pidcodes/pidcodes.github.com) をフォークする
2. このディレクトリの `org/` と `1209/` の中身を、フォークした作業ツリーの同じ場所にコピーする
3. コミットしてプルリクエストを送る（1 機器につき 1 PID。複数欲しい場合は理由の説明が要る）

## 受理されたあとにやること

`1209:0001`（テスト用 PID）から、割り当てられた番号に切り替える。

```
cmake -S firmware -B build/pico -G Ninja -DPICO_BOARD=pico -DHIDPIN_USB_PID=0x6870
cmake --build build/pico        # 書き込み直す
```

既定値そのものを変えるなら、次を書き換える。

- `firmware/CMakeLists.txt` の `HIDPIN_USB_PID` の既定値
- `host/src/hidpin/device.py` の `DEFAULT_PRODUCT_ID`
- `udev/70-hidpin.rules` の `idProduct`（2 行）
- `docs/PROTOCOL.md` §1、`docs/TESTING.md`、`README.md` の表記

書き換えたら、ファームウェアを書き込み直し、udev ルールを入れ直してボードを挿し直す。
