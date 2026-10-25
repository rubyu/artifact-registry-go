# Artifact lifecycle 契約

この文書と `dbcore.md` / `txscope.md` を Go 版の規範仕様とします。
元実装との対応は `docs/compatibility.md`、参照 commit は `docs/upstream.md` に記録します。

## 状態と公開

- object: `uploading`、`available`、`upload_failed`、`deleted`、`delete_failed`。
- publication: `staged`、`committed`、`discarding`、`discarded`、`aborted`。
- 通常 reader は `committed` と `available` の組だけを返します。
- bytes の内容 schema と業務データの意味は利用側が所有します。
- lifecycle timestamp は注入した `Clock` が決定します。
- timestamp は PostgreSQL と Python と同じ UTC / microsecond 精度に揃え、batch commit / abort は同じ timestamp を使用します。
- ref は opaque な UUID です。通常 API は object path / object ref を公開しません。
- digest は prefix を持たない lowercase SHA-256 hex 64 桁です。

## prepare / commit / abort

1. DB で UUIDv7 を採番します。
2. upload 前に、短い独立 transaction で durable intent を作成します。
3. 業務 transaction の外で object を upload します。
4. 短い transaction で upload を confirm します。
5. 利用側の業務 transaction と同じ `txscope.Scope` で publication を commit します。

`PrepareManyBytes` は fail-fast / all-success とし、部分的な prepared 結果を返しません。
全 item の upload 後、confirm を同じ transaction で行います。
ref の指定された unique 制約衝突に限り、作成済み intent を補償して最大 3 回の採番を行います。

`CommitPreparedMany` は savepoint 内で全 item を更新し、途中失敗で部分 commit を残しません。
prepared handle は生成元の `Registry` に束縛し、値コピー、再構成、別 Registry への転用を拒否します。
commit / abort は ref と object ref の対応も検証します。

prepare 成功後、commit hook 登録前の中止は `AbortPrepared` / `AbortPreparedMany` で補償します。
`AbortPreparedMany` の空入力は no-op とし、複数 item のエラーは処理を継続して `errors.Join` で返します。
既に `committed` / `discarding` / `discarded` / `aborted` の publication の abort は no-op です。
単独で transaction 外の publish を完了する API は提供しません。

## 失敗と cancellation

- intent 失敗は `PreparationError.Phase = publication_intent`。
- upload / confirm / transaction 内 I/O の認可拒否は `PreparationError.Phase = artifact_upload`。
- cancellation は `errors.Is(err, context.Canceled)` で判定できる形を維持します。
- 開始済み upload を完了させてから abort / delete を行います。
- upload 成功を確認できない object は `upload_failed` として maintenance に委ねます。
- 削除失敗は `delete_failed` に記録し、maintenance で retry します。
- commit 結果不明時は補償 hook を実行しません。DB の実状態を正とし、maintenance で収束します。

## lookup / read

malformed ref は lookup で未ヒットとし、status lookup では除外します。
discard は全 ref を検証してから変更し、malformed ref が一つでもあれば入力全体を拒否します。

committed handle は lookup transaction が成功した後だけ object read に使えます。
open / failed の scope、手作り・値コピーした handle を拒否します。
full read は長さを照合した後に digest を照合し、`IntegrityError.Reason` に
`size_mismatch` または `digest_mismatch` を設定します。DB 状態は変更しません。

range は `start >= 0`、`length > 0`、`start < size`、`length <= size-start` を必須とします。
短い応答、object 不在、storage error はエラーとします。range の digest 検証は保証しません。

## discard / maintenance

discard 結果は入力順・重複を保持します。同じ batch の同じ ref は同じ結果を返します。
`discard_requested`、`not_found`、`unsupported_state`、`already_discarding`、
`already_discarded`、`already_aborted` を使います。変更対象 ref は安定した順で処理します。

maintenance は通常 runtime と別 bundle / credential / 接続 pool を使います。
所有 pool は最大 2 接続、接続待ち 5 秒、connect timeout 5 秒、statement timeout 30 秒です。
同一 schema の cycle は advisory lock で直列化し、cycle 中は migration の shared lock も保持します。

cycle は stale staged、aborted、discarding、delete_failed の順で処理します。
各 phase の候補数は `MaxBatchSize` で制限し、retention / batch size / timeout は正の値を必須とします。
期限後に新しい item を開始しません。開始済み DELETE と状態更新は期限付きで完了させます。

存在確認に依存せず冪等 DELETE を実行します。object の削除と必要な publication 更新が成功してから件数を計上します。
`discarding` は object を `deleted` にしてから `discarded` へ進めます。
maintenance summary は phase 別件数、合計、残 backlog と最古の待ち時間、cycle / lock の所要時間を持ちます。
削除失敗件数が正の場合、maintenance CLI は summary JSON と exit code `2` を返します。
