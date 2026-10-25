package txscope

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type State string

const (
	Open      State = "open"
	Succeeded State = "closed_succeeded"
	Failed    State = "closed_failed"
)

var (
	ErrNotOpen      = errors.New("transaction scope is not open")
	ErrNotSucceeded = errors.New("transaction has not succeeded")
	ErrExternalIO   = errors.New("external I/O in an open transaction is forbidden")
)

type Hook func(context.Context) error
type Backend interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	Commit() error
	Rollback() error
}
type Scope struct {
	mu                sync.Mutex
	backend           Backend
	state             State
	failed, succeeded []Hook
	ctx               context.Context
	parent            *Scope
}
type contextKey struct{}
type permissionKey struct{}
type permission struct {
	scope  *Scope
	active atomic.Bool
}
type Row interface{ Scan(...any) error }
type errorRow struct{ err error }

func (r errorRow) Scan(...any) error { return r.err }

func (s *Scope) State() State { s.mu.Lock(); defer s.mu.Unlock(); return s.state }
func (s *Scope) RequireOpen() error {
	if s.State() != Open {
		return ErrNotOpen
	}
	return nil
}
func (s *Scope) RequireSucceeded() error {
	if s.State() != Succeeded {
		return ErrNotSucceeded
	}
	return nil
}
func (s *Scope) Context() context.Context { return s.ctx }
func (s *Scope) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if err := s.RequireOpen(); err != nil {
		return nil, err
	}
	return s.backend.ExecContext(ctx, q, args...)
}
func (s *Scope) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if err := s.RequireOpen(); err != nil {
		return nil, err
	}
	return s.backend.QueryContext(ctx, q, args...)
}
func (s *Scope) QueryRowContext(ctx context.Context, q string, args ...any) Row {
	if err := s.RequireOpen(); err != nil {
		return errorRow{err}
	}
	return s.backend.QueryRowContext(ctx, q, args...)
}
func (s *Scope) OnFailed(h Hook) error    { return s.add(h, false) }
func (s *Scope) OnSucceeded(h Hook) error { return s.add(h, true) }
func (s *Scope) add(h Hook, success bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != Open {
		return ErrNotOpen
	}
	if h == nil {
		return errors.New("nil transaction hook")
	}
	if success {
		s.succeeded = append(s.succeeded, h)
	} else {
		s.failed = append(s.failed, h)
	}
	return nil
}

func AssertExternalIOAllowed(ctx context.Context) error {
	s, _ := ctx.Value(contextKey{}).(*Scope)
	if permit, ok := ctx.Value(permissionKey{}).(*permission); ok && permit.scope == s && permit.active.Load() {
		return nil
	}
	for current := s; current != nil; current = current.parent {
		if current.State() == Open {
			return ErrExternalIO
		}
	}
	return nil
}

// ExternalIOInTx は理由を持つ閉じた副作用処理にだけ、有効期間付きの許可を渡す。
func ExternalIOInTx(ctx context.Context, reason string, fn func(context.Context) error) error {
	if reason == "" || fn == nil {
		return errors.New("external I/O requires a reason and callback")
	}
	s, _ := ctx.Value(contextKey{}).(*Scope)
	permit := &permission{scope: s}
	permit.active.Store(true)
	defer permit.active.Store(false)
	return fn(context.WithValue(ctx, permissionKey{}, permit))
}

type sqlBackend struct {
	*sql.Tx
	conn *sql.Conn
}

func (b *sqlBackend) complete(commit bool) error {
	var err error
	if commit {
		err = b.Tx.Commit()
	} else {
		err = b.Tx.Rollback()
	}
	if err != nil {
		_ = b.conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	closeErr := b.conn.Close()
	if errors.Is(closeErr, sql.ErrConnDone) {
		closeErr = nil
	}
	return errors.Join(err, closeErr)
}
func (b *sqlBackend) Commit() error   { return b.complete(true) }
func (b *sqlBackend) Rollback() error { return b.complete(false) }

// Run は transaction の終了を所有する。body は scope.Context を下位処理へ渡す。
func Run(ctx context.Context, db *sql.DB, fn func(*Scope) error) error {
	poolContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	conn, err := db.Conn(poolContext)
	cancel()
	if err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		conn.Close()
		return err
	}
	return RunBackend(ctx, &sqlBackend{Tx: tx, conn: conn}, fn)
}

// RunBackend は接続 adapter と fault injection に同じ状態機械を適用する。
func RunBackend(ctx context.Context, backend Backend, fn func(*Scope) error) (result error) {
	s := &Scope{backend: backend, state: Open}
	s.parent, _ = ctx.Value(contextKey{}).(*Scope)
	s.ctx = context.WithValue(ctx, contextKey{}, s)
	defer func() {
		if value := recover(); value != nil {
			if s.State() == Open {
				_ = s.finish(fmt.Errorf("panic: %v", value))
			}
			panic(value)
		}
	}()
	bodyErr := fn(s)
	if bodyErr == nil {
		bodyErr = ctx.Err()
	}
	if bodyErr != nil {
		return s.finish(bodyErr)
	}
	if _, err := backend.ExecContext(ctx, "SELECT 1"); err != nil {
		return s.finish(err)
	}
	if err := backend.Commit(); err != nil {
		// commit の成否不明時には補償 hook を実行しない。
		s.close(Failed)
		return err
	}
	hooks := s.close(Succeeded)
	return runHooks(ctx, hooks)
}

func (s *Scope) finish(primary error) error {
	rollbackErr := s.backend.Rollback()
	hooks := s.close(Failed)
	if rollbackErr != nil {
		return errors.Join(primary, rollbackErr)
	}
	return errors.Join(primary, runHooks(s.ctx, hooks))
}
func (s *Scope) close(state State) []Hook {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != Open {
		return nil
	}
	s.state = state
	hooks := s.failed
	if state == Succeeded {
		hooks = s.succeeded
	}
	s.failed = nil
	s.succeeded = nil
	return hooks
}
func runHooks(ctx context.Context, hooks []Hook) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	var errs []error
	for _, hook := range hooks {
		if err := hook(cleanup); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type ScopedValue[T any] struct {
	scope *Scope
	value T
}

func Bind[T any](scope *Scope, value T) ScopedValue[T] { return ScopedValue[T]{scope, value} }
func (v ScopedValue[T]) RequireSucceeded() (T, error) {
	if v.scope == nil {
		var zero T
		return zero, ErrNotSucceeded
	}
	if err := v.scope.RequireSucceeded(); err != nil {
		var zero T
		return zero, err
	}
	return v.value, nil
}
