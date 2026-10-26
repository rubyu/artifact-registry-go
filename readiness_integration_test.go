package artifactregistry

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/rubyu/artifact-registry-go/dbcore"
	"github.com/rubyu/artifact-registry-go/schema"
)

func TestIntegrationSchemaReadySingleConnection(t *testing.T) {
	h := integrationHarness(t)
	connection := h.config.DB
	connection.User = h.roles.Readiness
	connection.Password = envOr("ARTIFACT_REGISTRY_TEST_READINESS_PASSWORD", "test-only")
	db, err := connection.Open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	key := "artifact-registry/" + h.config.Schema
	bodyErr := errors.New("callback failed")
	for _, test := range []struct {
		name     string
		version  int64
		bodyErr  error
		wantErr  error
		wantCall bool
	}{
		{"success", schema.Version, nil, nil, true},
		{"callback error", schema.Version, bodyErr, bodyErr, true},
		{"revision mismatch", schema.Version + 1, nil, dbcore.ErrSchemaVersion, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			called := false
			err := dbcore.WithSchemaReady(ctx, db, h.config.Schema, key, test.version, time.Second, func(ctx context.Context, conn *sql.Conn) error {
				called = true
				var value int
				if err := conn.QueryRowContext(ctx, "SELECT 1").Scan(&value); err != nil {
					return err
				}
				other, err := h.admin.Conn(ctx)
				if err != nil {
					return err
				}
				defer other.Close()
				var acquired bool
				if err := other.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", dbcore.LockID(key)).Scan(&acquired); err != nil {
					return err
				}
				if acquired {
					_, _ = other.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", dbcore.LockID(key))
					t.Error("migration lock was not held during callback")
				}
				return test.bodyErr
			})
			if !errors.Is(err, test.wantErr) || called != test.wantCall {
				t.Fatalf("err=%v, called=%v; want err=%v, called=%v", err, called, test.wantErr, test.wantCall)
			}
			// 成功・callback 失敗・revision 不一致のいずれでも lock と connection を返す。
			if stats := db.Stats(); stats.InUse != 0 || stats.WaitCount != 0 {
				t.Fatalf("readiness reserved another connection or leaked one: %+v", stats)
			}
			lock, err := dbcore.AcquireLock(ctx, h.admin, key, false, time.Second)
			if err != nil {
				t.Fatalf("migration lock was not released: %v", err)
			}
			if err = lock.Release(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
