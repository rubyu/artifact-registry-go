# DB 基盤と migration 契約

PostgreSQL 18 以降を使用します。object と publication の table set は同じ migration 系列で進めます。
migration は Goose の Go migration と埋め込み SQL を使用し、revision 1 / 2 は移植元の revision 1 / 2 に対応します。
適用済み revision は変更せず、新しい forward migration を追加します。downgrade は実行前にエラーとします。

`schema.BuildTarget` は migration target を構築し、実行しません。
`dbcore.EnsureLatest` は対象 schema の exclusive advisory lock を保持して実行します。
通常処理は同じ key の shared lock を保持し、期待 revision と DB の revision が一致した場合だけ処理を進めます。
Go runtime の構築だけで migration は実行しません。

lock key は UTF-8 の BLAKE2b、digest size 8 byte、big endian signed 64-bit に変換します。
schema の既定 key は `artifact-registry/<schema>`、cleanup key は `artifact-registry/cleanup/<schema>` です。
session lock は専用 connection で保持し、解放を確認できない connection は pool に戻しません。
lock の取得・解放は有界とし、元エラーと解放エラーの両方を返します。

role-aware target は owner、migrator、runtime、maintenance、readiness の五 role を必須とし、重複を拒否します。
identifier は lowercase ASCII の先頭文字または `_` で始まり、以降は lowercase ASCII / 数字 / `_` とします。
63 byte を超える identifier を拒否し、SQL の identifier を引用します。

接続は migrator credential を使用し、owner への `SET ROLE` を再接続時にも適用します。
session_user は接続主体のまま保持します。role の作成・membership・database privilege は bootstrap が所有します。
CLI は bootstrap 済み owner 所有 schema を使い、schema を自動作成しません。
programmatic target の role 未指定経路と schema 作成指定は、明示した場合だけ使用します。

runtime / maintenance は lifecycle table の列単位 SELECT と、担当 lifecycle 関数の EXECUTE のみを持ちます。
direct DML、table 全体 SELECT、identity sequence、enum USAGE は付与しません。
lifecycle 関数は owner 所有の `SECURITY DEFINER` とし、固定 `search_path` と session_user guard を持ちます。
readiness は migration 管理テーブルだけを SELECT します。runtime は version 判定用の列だけを SELECT します。
migrator / readiness / PUBLIC に lifecycle 関数の EXECUTE を付与しません。
PUBLIC と application role に将来 object の broad default privilege を作りません。

`schema_migrations` は Goose の管理データです。Alembic marker を検出した場合、自動採用を拒否します。
既存 DB の採用、owner 移管、marker 切り替えは個別にレビューした移行手順で行います。

UUIDv7 は caller-owned connection 上の PostgreSQL `uuidv7()` で採番します。
digest helper は prefix を持たない lowercase SHA-256 hex の計算・検証・CHECK 式を提供します。
