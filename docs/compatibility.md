# Python 参照実装との互換性

参照 commit は [upstream.md](upstream.md) に固定します。
互換性の対象は保存データ、状態遷移、公開条件、transaction の原子性、失敗後の収束、DB 権限です。
Python の API 名、例外クラス名、task-local 実装、migration 管理形式は Go の API に置き換えます。

## Go の API

- `prepare_bytes` → `Registry.PrepareBytes`
- `prepare_many_bytes` → `Registry.PrepareManyBytes`
- `commit_prepared*` / `abort_prepared*` → `Registry.CommitPrepared*` / `AbortPrepared*`
- reader → `Registry.LookupCommittedPublication` / `LookupPublicationStatuses`
- object reader → `Registry.ReadBytes` / `ReadRangeBytes`
- discard service → `Registry.RequestDiscardMany`
- diagnostic reader → `Advanced.LookupPublicationDiagnostic`
- maintenance service → `Maintenance.RunCleanupCycle`

prepared / committed handle の情報は `Info()` で取得します。
返された handle の値コピー、JSON からの再構成、別 publisher への prepared handle の転用は拒否します。
通常 API の情報には object path / object ref を含めません。

## migration の境界

Go 側は Goose の `schema_migrations` を使用し、Alembic を実行しません。
既存 Alembic 管理 DB を検出した場合はエラーにし、勝手に stamp / version 書き換えを行いません。
既存 DB の採用には、catalog の比較、role/ACL、既存 revision、稼働中の Python workload を確認した
個別の切り替え手順が必要です。新規 DB の構築と既存 DB の採用は別の互換性項目です。

Python と Go の比較には独立した schema を使用します。
UUID、timestamp、内部 object path、migration 管理テーブル名は正規化し、公開状態と結果を比較します。
Go 側の migration revision 1 / 2 は移植元の revision 1 / 2 に対応します。

## cancellation と context

Go では `txscope.Scope.Context()` を下位処理へ明示的に渡します。
`context.Background()` への置き換えで scope を隠す呼び出しは契約外です。
開始済みの storage I/O と補償は、親の cancellation 後も期限付き context で完了させます。
commit 結果不明の場合は hook を実行せず、durable state と maintenance に委ねます。

動的 S3 credential は `ObjectStoreConfig.Credentials` に MinIO credential provider を注入します。
provider は TLS を必須とし、固定 credential と併用できません。
元プロジェクト固有の identity-role / secret provider は利用側が所有します。

## 回帰検証

`registry_integration_test.go` は実 PostgreSQL と S3 API を使用し、公開、lookup、range、integrity、
rollback、batch 原子性、upload 失敗、cancellation、stale cleanup、delete retry、lock、権限拒否を検証します。
`txscope/scope_test.go` は commit / rollback 結果不明、probe、hook の順序と元エラーの維持を検証します。
Python との比較で固定した結果は `testdata/compatibility/` と Go テストで再検証します。
