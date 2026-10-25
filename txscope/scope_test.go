package txscope

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
)

type fakeBackend struct {
	probe, commit, rollback error
	calls                   []string
}

func (b *fakeBackend) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	b.calls = append(b.calls, "probe")
	return driver.RowsAffected(1), b.probe
}
func (b *fakeBackend) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, nil
}
func (b *fakeBackend) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }
func (b *fakeBackend) Commit() error                                            { b.calls = append(b.calls, "commit"); return b.commit }
func (b *fakeBackend) Rollback() error                                          { b.calls = append(b.calls, "rollback"); return b.rollback }

func TestCompletionAndHooks(t *testing.T) {
	primary := errors.New("primary")
	secondary := errors.New("secondary")
	for _, test := range []struct {
		name    string
		backend fakeBackend
		body    error
		want    State
		calls   []string
		hooks   bool
	}{
		{"success", fakeBackend{}, nil, Succeeded, []string{"probe", "commit"}, true},
		{"body failure", fakeBackend{}, primary, Failed, []string{"rollback"}, true},
		{"probe failure", fakeBackend{probe: primary}, nil, Failed, []string{"probe", "rollback"}, true},
		{"commit unknown", fakeBackend{commit: primary}, nil, Failed, []string{"probe", "commit"}, false},
		{"rollback unknown", fakeBackend{rollback: secondary}, primary, Failed, []string{"rollback"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var scope *Scope
			var hooks []string
			var value ScopedValue[string]
			err := RunBackend(context.Background(), &test.backend, func(s *Scope) error {
				scope = s
				value = Bind(s, "value")
				if _, err := value.RequireSucceeded(); !errors.Is(err, ErrNotSucceeded) {
					t.Fatal("open scope exposed value")
				}
				if !errors.Is(AssertExternalIOAllowed(s.Context()), ErrExternalIO) {
					t.Fatal("external I/O allowed")
				}
				s.OnFailed(func(context.Context) error { hooks = append(hooks, "failed"); return nil })
				s.OnSucceeded(func(context.Context) error { hooks = append(hooks, "succeeded"); return nil })
				return test.body
			})
			if scope.State() != test.want {
				t.Fatalf("state: %s", scope.State())
			}
			if !reflect.DeepEqual(test.backend.calls, test.calls) {
				t.Fatalf("calls: %v", test.backend.calls)
			}
			if test.want == Succeeded {
				if err != nil {
					t.Fatal(err)
				}
				if got, err := value.RequireSucceeded(); err != nil || got != "value" {
					t.Fatal(got, err)
				}
			} else {
				if !errors.Is(err, primary) {
					t.Fatalf("primary lost: %v", err)
				}
				if _, err := value.RequireSucceeded(); !errors.Is(err, ErrNotSucceeded) {
					t.Fatal("failed scope exposed value")
				}
			}
			if (len(hooks) > 0) != test.hooks {
				t.Fatalf("hooks: %v", hooks)
			}
			if scope.OnFailed(func(context.Context) error { return nil }) != ErrNotOpen {
				t.Fatal("terminal hook registration allowed")
			}
		})
	}
}
func TestHookOrderAndFailurePrecedence(t *testing.T) {
	primary, hookErr := errors.New("body"), errors.New("hook")
	var order []int
	err := RunBackend(context.Background(), &fakeBackend{}, func(s *Scope) error {
		for i := 0; i < 3; i++ {
			i := i
			s.OnFailed(func(ctx context.Context) error {
				if err := ctx.Err(); err != nil {
					t.Fatal(err)
				}
				order = append(order, i)
				return hookErr
			})
		}
		return primary
	})
	if !errors.Is(err, primary) || !errors.Is(err, hookErr) || !reflect.DeepEqual(order, []int{0, 1, 2}) {
		t.Fatal(err, order)
	}
}
func TestCancellationAndPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ran := false
	err := RunBackend(ctx, &fakeBackend{}, func(s *Scope) error {
		s.OnFailed(func(ctx context.Context) error { ran = true; return ctx.Err() })
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || !ran {
		t.Fatal(err, ran)
	}
	var scope *Scope
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic swallowed")
			}
		}()
		RunBackend(context.Background(), &fakeBackend{}, func(s *Scope) error { scope = s; panic("boom") })
	}()
	if scope.State() != Failed {
		t.Fatal(scope.State())
	}
}
