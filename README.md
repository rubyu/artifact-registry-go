# artifact-registry-go

PostgreSQL と S3 互換 object storage を組み合わせ、artifact の物理状態と公開状態を管理する Go ライブラリです。
Go 1.26 以降、PostgreSQL 18 以降を使用します。

```sh
go get github.com/rubyu/artifact-registry-go
```

`PrepareBytes` / `PrepareManyBytes` で upload を完了し、利用側の業務 transaction 内で
`CommitPrepared` / `CommitPreparedMany` を呼び出します。公開が成功した transaction の後にだけ
`ReadBytes` / `ReadRangeBytes` を実行します。利用例は [examples](examples/) を参照してください。

```go
prepared, err := registry.PrepareBytes(ctx, payload)
if err != nil {
    return err
}
err = registry.RunTx(ctx, func(tx *txscope.Scope) error {
    // 業務 row の更新も tx.ExecContext(tx.Context(), ...) で同時に行う。
    _, err := registry.CommitPrepared(tx.Context(), tx, prepared)
    return err
})
```

準備後、commit hook を登録する前に処理を中止した場合は `AbortPrepared` を実行してください。
commit の結果が確認できない場合は即時補償せず、durable state と maintenance で収束します。
通常 runtime は cleanup API を持たず、専用の `Maintenance` を別 credential で構築します。

## 構成

- root package: prepare / commit / abort / lookup / read / discard、通常・advanced・maintenance の各 bundle。
- `dbcore`: PostgreSQL 接続、schema readiness、migration、advisory lock、UUIDv7、SHA-256。
- `txscope`: transaction の所有、完了状態、成功・失敗 hook、成功後の値取得。
- `schema`: Goose による forward-only migration と埋め込み SQL。
- `cmd`: migration / maintenance CLI。
- `spec`: Go 版の規範仕様。
- `testdata/compatibility`: Python 参照実装との比較結果から固定した検証データ。

## 検証

```sh
go test ./...
go vet ./...
docker compose -f docker/compose.yaml up -d --wait
make test-integration
docker compose -f docker/compose.yaml down -v
```

統合テストは専用 DB に schema を作成・削除します。指定する接続は検証専用にしてください。
`ARTIFACT_REGISTRY_TEST_POSTGRES_DSN` が未設定の場合、統合テストは skip します。
CI は PostgreSQL と Silo を起動し、統合テストを含めて `go test -race` を実行します。

## CLI と migration

```sh
go build ./cmd/...
go run ./cmd/artifact-registry-migrate
go run ./cmd/artifact-registry-maintenance
```

環境変数と権限は [運用手順](docs/operations.md) を参照してください。
runtime の構築時に migration は実行しません。migration CLI は bootstrap 済み schema と五つの専用 role を要求します。
Alembic 管理 DB の自動採用は行いません。[互換性と移行範囲](docs/compatibility.md) を参照してください。

## リリース

`main` の更新で直近の安定タグから minor を一つ増やし、patch を `0` に戻します。
最初のタグは `v0.1.0` です。ソースに version 更新 commit を追加せず、対象 commit にタグを付けます。
Linux / macOS / Windows 向け CLI と SHA-256 checksum、`version.json` を GitHub Release に公開します。
各 CLI の `--version` には version、完全な commit hash、対象 commit の日時を埋め込みます。
同じ commit の再実行は同じタグを使用します。Go ライブラリはそのタグで取得できます。

移植元は [docs/upstream.md](docs/upstream.md) を参照してください。
