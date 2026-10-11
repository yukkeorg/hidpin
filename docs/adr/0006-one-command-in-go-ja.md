---
status: accepted
---

# hidpin コマンドは Go 版の 1 つだけにし、tools/hidpin に置く

[English](0006-one-command-in-go.md) | **日本語**

`hidpin` コマンドは Python（`python/src/hidpin/cli.py`）と Go（`cmd/hidpin`）の 2 つがあり、コマンド、オプション、出力が同じだった。コマンドを変えるたびに、両方で直し、テストし、文書に書く必要があった。実際に両者はずれ始めていた（一時期は Go 版だけがボードの抜き差しに耐えた）。
同じものが複数あると、それぞれにメンテナンスが要る。そのため、コマンドは 1 つだけ残す。Go 版を正式な `hidpin` コマンドとし、Python 版のコマンドとそのテストは削除する。`python/` はホストライブラリだけになる。
コマンドのソースは `cmd/hidpin` から `tools/hidpin` に移し、ツールの開発を `tools/` の下で管理する。Go のモジュールはリポジトリ直下のままで、インストールは `go install github.com/yukkeorg/hidpin/tools/hidpin@latest` になる。

## 検討した選択肢

- 両方のコマンドを残す: 上の理由で採らない。
- 代わりに Python 版を残す: 選ばない。正式なコマンドは Go 版とする。
- `tools/` に専用の Go モジュールを作る: コマンドが同じモジュールのホストライブラリにしか依存しないので、今は必要ない。

## 結果

- コマンドのインストールには、Python の代わりに Go（1.22 以上）が要る。Go のホストライブラリと同じく Linux 専用である。
- コマンドの変更は `tools/hidpin` だけで済む。テストは `hidpin/hidpintest` の偽のデバイスと模擬バスで動き、実機は要らない。
- この判断はコマンドについてのもので、ホストライブラリには及ばない。Python、Go、Rust はそれぞれホストライブラリを持ち続け、docs/HOST_API.md で同じ API に、`protocol/vectors.json` で同じ通信形式にそろえる。
- ADR 0001、0002、0004 には「CLI」「両方の CLI」という記述が残る。コマンドが 2 つあった時点の判断の記録なので、そのままにする。
