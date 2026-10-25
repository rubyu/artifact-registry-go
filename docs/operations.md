# 運用と環境変数

## bootstrap / migration

bootstrap 管理者が owner、migrator、runtime、maintenance、readiness の五 role と schema を作成します。
owner は NOLOGIN、他の role は LOGIN NOINHERIT とし、migrator にだけ owner への SET を許可します。
検証用の具体例は `docker/init-roles.sql` にあります。
production の role、membership、database privilege、secret の設定は利用側が所有します。

migration CLI は次の値をすべて要求します。

- `ARTIFACT_REGISTRY_MIGRATION_POSTGRES_HOST`
- `ARTIFACT_REGISTRY_MIGRATION_POSTGRES_PORT`
- `ARTIFACT_REGISTRY_MIGRATION_POSTGRES_USER`
- `ARTIFACT_REGISTRY_MIGRATION_POSTGRES_PASSWORD`
- `ARTIFACT_REGISTRY_MIGRATION_POSTGRES_DATABASE`
- `ARTIFACT_REGISTRY_DATABASE_OWNER_ROLE`
- `ARTIFACT_REGISTRY_DATABASE_MIGRATOR_ROLE`
- `ARTIFACT_REGISTRY_DATABASE_RUNTIME_ROLE`
- `ARTIFACT_REGISTRY_DATABASE_MAINTENANCE_ROLE`
- `ARTIFACT_REGISTRY_DATABASE_READINESS_ROLE`

`ARTIFACT_REGISTRY_SCHEMA` の既定値は `artifact_registry` です。
migration の接続 user 名と migrator 権限 role 名が異なる動的 LOGIN も使用できます。
その場合も owner への SET 経路を PostgreSQL 側で要求します。
他 workload の credential への fallback は行いません。
migration CLI の stdout は `status` と `steps` を持ち、失敗時は `message` を追加します。
各 step は target / schema、適用前後の revision、開始した action、失敗理由を保持します。
診断上の revision は移植元との対応を明確にするため 12 桁表記にします。

## maintenance

接続には `ARTIFACT_REGISTRY_MAINTENANCE_POSTGRES_` の HOST / PORT / USER / PASSWORD / DATABASE を使います。
USER と `ARTIFACT_REGISTRY_DATABASE_MAINTENANCE_ROLE` の一致を要求します。
object storage は次の値を使います。

- `ARTIFACT_REGISTRY_S3_ENDPOINT`: `host:port`。
- `ARTIFACT_REGISTRY_S3_ACCESS_KEY` / `ARTIFACT_REGISTRY_S3_SECRET_KEY`。
- `ARTIFACT_REGISTRY_S3_BUCKET`。
- `ARTIFACT_REGISTRY_S3_SECURE`: `1` / `true` / `TRUE` / `yes` で TLS。有効値の既定は `0`。
- `ARTIFACT_REGISTRY_BASE_PREFIX`: 既定 `artifact-registry`。
- `ARTIFACT_REGISTRY_S3_CONNECT_TIMEOUT_SECONDS`: 既定 `3`。
- `ARTIFACT_REGISTRY_S3_READ_TIMEOUT_SECONDS`: 既定 `10`。
- `ARTIFACT_REGISTRY_S3_RETRY_COUNT`: 既定 `2`。非負整数。

cleanup の設定は次のとおりです。いずれも正の値を必須とします。

| 環境変数 | 既定値 |
| --- | --- |
| `ARTIFACT_REGISTRY_STAGED_RETENTION_HOURS` | 24 |
| `ARTIFACT_REGISTRY_ABORTED_RETENTION_HOURS` | 24 |
| `ARTIFACT_REGISTRY_CLEANUP_MAX_BATCH_SIZE` | 100 |
| `ARTIFACT_REGISTRY_CLEANUP_LOCK_TIMEOUT_SECONDS` | 5 |
| `ARTIFACT_REGISTRY_CLEANUP_CYCLE_TIMEOUT_SECONDS` | 600 |

`DEPLOYMENT_STAGE` が未設定、`local`、`testing`、`ci` の場合、PostgreSQL TLS を disable にします。
それ以外は require にします。programmatic `dbcore.Config` は verify-ca / verify-full も受け取ります。

maintenance CLI は JSON を stdout に出します。通常成功は exit `0`、削除失敗を含む cycle は `2`、
設定・DB・lock・cycle timeout などの失敗は `1` です。
consumer の production 定期実行は、consumer 自身の CLI / CronJob として運用してください。

## ローカル比較

Python の参照環境は repository 外に置き、`docs/upstream.md` の commit を checkout します。
参照元が指定する SQLAlchemy 2.0.43 / asyncpg 0.30.0 などの依存を使用し、別 schema に migration します。
比較した正常系・補償・値・catalog の結果を `testdata/compatibility/python-reference.json` に固定しています。
catalog の取得 SQL は同じフォルダの `catalog-queries.json` にあります。
schema 名・引用符・空白を正規化し、migration 管理テーブルを除いた lifecycle の構造・関数・権限を比較します。
ローカル比較では PostgreSQL 18.6 と S3 emulator、CI では PostgreSQL 18 と Silo を使用します。
