# Transaction scope 契約

`txscope.Run` は一つの root transaction の終了を所有します。
scope の状態は `open`、`closed_succeeded`、`closed_failed` とします。
open からいずれかの terminal state にだけ進みます。

body の正常終了、同じ transaction での一回の usability probe、commit 成功を確認した場合だけ succeeded とします。
probe は application row / catalog / role / object storage を読まず、`SELECT 1` を実行します。
body / probe の失敗後、rollback 成功を確認した場合だけ failed hook を登録順に実行します。
commit または rollback の結果不明、root transaction の早期終了を検出した場合は、両方の hook を破棄します。
failed は rollback 完了や DB row 不在を意味しません。

commit 成功を確認して状態を succeeded にしてから succeeded hook を実行します。
hook 失敗によって DB commit の成否を変更しません。
元エラーと複数 hook エラーを `errors.Join` で保持し、後続 hook の実行を継続します。
hook の後に参照を保持しません。terminal state への hook 登録を拒否します。

panic の場合も rollback を試み、完了状態を設定してから同じ panic を再送出します。
cancellation の判定は `errors.Is` を維持し、補償には期限付き context を使用します。
`ScopedValue.RequireSucceeded` は scope の成功後だけ値を返します。

DB 操作は `Scope.ExecContext` / `QueryContext` / `QueryRowContext` から行います。
呼び出し元による raw SQL の COMMIT / ROLLBACK、transaction の置き換えは契約外です。
scope をコピーしたり複数 goroutine で transaction lifecycle を操作してはなりません。

下位処理には `Scope.Context()` を渡します。Go では task-local 変数を使用しません。
open scope 中の外部 I/O は拒否し、nested scope 終了後も外側の open scope を確認します。
advanced exception は `ExternalIOInTx` に理由と閉じた callback を渡した場合だけ許可します。
許可用 context は callback の終了後に失効します。外部 I/O を行う child 処理は callback 内で完了させます。
この例外の利用側は即時補償または durable state と maintenance による収束を所有します。
