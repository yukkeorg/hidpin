---
status: accepted
---

# USB の識別番号に pid.codes の 1209:6870 を使う

[English](0003-pid-codes-usb-ids.md) | **日本語**

hidpin のホストのツールは VID:PID でデバイスを探し、udev ルールも VID:PID で権限を与える。そのため、ほかの機器と重ならない専用の番号が要る。
pid.codes（VID `0x1209`）は、ソースを公開した OSS のハードウェア・ソフトウェアに PID を無償で割り当てており、hidpin（MIT ライセンス、GitHub で公開）はその条件を満たす。
PID には、hidpin の頭文字を ASCII で表した `0x6870`（"hp"）を選んだ。申請時（2026-09-17）に、登録済みの 921 件とオープンなプルリクエスト 85 件のどちらとも重ならないことを確かめた。
申請（[PR #1280](https://github.com/pidcodes/pidcodes.github.com/pull/1280)）は 2026-09-28 に受理され、登録内容は <https://pid.codes/1209/6870/> にある。

## Considered Options

- pid.codes のテスト用 PID `1209:0001` を使い続ける: 社内でのテスト専用で、再配布・販売・製造する機器には使えない。ほかのテスト機器と重なることもある。
- USB-IF から VID を取得する: 6,000 ドルかかり、個人の OSS には見合わない。
- Raspberry Pi の VID `0x2E8A` で PID の割り当てを受ける: Raspberry Pi のチップを使う製品向けの制度で、HID など標準のドライバで動く機器には、共通の PID を使って製品名の文字列で見分けることを勧めている。番号がチップの供給元に結びつき、チップに依存しない名前にした方針（ADR 0001）とも合わない。

## Consequences

- ファームウェアを改造した機器を配布する人は、自分の PID を取得する（README の「USB の識別番号について」）。手元での試作にはテスト用 PID を使える。
- 登録内容（説明文や URL）を変えるときは、pid.codes のリポジトリの `1209/6870/index.md` と `org/yukke.org/index.md` に対してプルリクエストを出す。
  申請時の下書きは `docs/pid-codes/` にあったが、登録された内容と同じだったため削除した。git の履歴には残っている。
