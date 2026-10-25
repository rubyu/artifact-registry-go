package txscope

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
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
}
type contextKey struct{}

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
func (s *Scope) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
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
	if s, ok := ctx.Value(contextKey{}).(*Scope); ok && s.State() == Open {
		return ErrExternalIO
	}
	return nil
}

// Run は transaction の終了を所有する。body は scope.Context を下位処理へ渡す。
func Run(ctx context.Context, db *sql.DB, fn func(*Scope) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	return RunBackend(ctx, tx, fn)
}

// RunBackend は接続 adapter と fault injection に同じ状態機械を適用する。
func RunBackend(ctx context.Context, backend Backend, fn func(*Scope) error) (result error) {
	s := &Scope{backend: backend, state: Open}
	s.ctx = context.WithValue(ctx, contextKey{}, s)
	defer func() {
		if value := recover(); value != nil {
			_ = s.finish(fmt.Errorf("panic: %v", value), false)
			panic(value)
		}
	}()
	bodyErr := fn(s)
	if bodyErr == nil {
		bodyErr = ctx.Err()
	}
	if bodyErr != nil {
		return s.finish(bodyErr, false)
	}
	if _, err := backend.ExecContext(ctx, "SELECT 1"); err != nil {
		return s.finish(err, false)
	}
	if err := backend.Commit(); err != nil {
		// commit の成否不明時には補償 hook を実行しない。
		s.close(Failed)
		return err
	}
	hooks := s.close(Succeeded)
	return runHooks(ctx, hooks)
}

func (s *Scope) finish(primary error, _ bool) error {
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
