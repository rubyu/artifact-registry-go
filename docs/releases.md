# リリース手順

Go 本体を取り込んだ後は、`main` の更新ごとにテスト、CLI のビルド、GitHub Release の公開を実行します。
workflow を本体より先に導入できるよう、`go.mod` がまだない commit では公開をスキップします。

## バージョンの指定

通常は最新の安定タグの patch を一つ増やします。例: `v0.1.0` → `v0.1.1`。
機能追加や破壊的変更をまとめてリリースする場合は、**main 向け PR** にマージ前にラベルを付けます。

| main 向け PR のラベル | 更新 | 例 |
| --- | --- | --- |
| 指定なし | patch + 1 | `v0.1.2` → `v0.1.3` |
| `release:minor` | minor + 1、patch を 0 にする | `v0.1.2` → `v0.2.0` |
| `release:major` | major + 1、minor / patch を 0 にする | `v0.1.2` → `v1.0.0` |

両方のラベルがある場合は major を優先します。ラベル名は完全一致で判定します。
ラベルがまだリポジトリに存在しない場合は、同じ名前で作成してください。
初回はラベルの有無にかかわらず `v0.1.0` です。

`develop` 向けの個別 PR のラベルは集計しません。`develop` → `main` の PR で、
取り込む変更全体に合うラベルを指定してください。main への直接 push は patch として扱います。
機能追加・破壊的変更のラベルを忘れると patch になるため、main 向け PR のレビューで確認します。
Go module の v2 以降では、major version に対応する module / import path の更新も必要です。

## 成果物と再実行

対象 commit に version タグを付け、Linux / macOS / Windows 向け CLI、SHA-256 checksum、
`version.json` を公開します。CLI の `--version` には version、完全な commit hash、
対象 commit の日時を埋め込みます。version 更新用の commit は追加しません。

同じ commit を再実行する場合は既存タグを再利用します。後からラベルを変更しても新しい番号は採番しません。
PR 情報の取得・解析に失敗した場合は公開を停止します。

## ローカル検証

標準ライブラリと Git だけを使うため、Go module の取り込み前でもテストできます。

```sh
cd tools/release
go test -race -count=1 main.go main_test.go
```
