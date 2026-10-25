# 移植元

- repository: https://github.com/rubyu/vivid47
- branch: `develop`
- commit: `46d82c12a9323a2c013c272fbf845891b5602322`
- 対象: `backend/libs/artifact-registry`、`backend/libs/db-core`、`backend/libs/tx-scope`

Python コードと比較実行用の Python 環境は、この repository の外で管理します。
`spec-tooling` と Lean 検証ワークスペースは取り込みません。

`schema/migrations/00001_tables.sql` と `00002_lifecycle.sql` は、移植元 revision 1 / 2 の SQL 定数から抽出しました。
`ownership.sql` と `privileges.sql` は同 revision 2 の renderer から抽出し、migration 管理テーブルを
`alembic_version` から Goose の `schema_migrations` に置き換えています。
それ以外の lifecycle table、enum、制約、trigger、関数、role 境界は移植元を基準に維持します。

`docker/init-roles.sql` は移植元の検証用 bootstrap です。記載の password は隔離した検証環境専用です。
