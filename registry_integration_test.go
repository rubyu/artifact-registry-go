package artifactregistry

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rubyu/artifact-registry-go/dbcore"
	"github.com/rubyu/artifact-registry-go/schema"
	"github.com/rubyu/artifact-registry-go/txscope"
)

type fixedClock struct{ value time.Time }

func (c *fixedClock) Now() time.Time { return c.value }

type harness struct {
	admin  *sql.DB
	r      *Registry
	m      *Maintenance
	config Config
	clock  *fixedClock
	roles  schema.Roles
}

var bootstrapOnce sync.Once
var bootstrapError error

func integrationHarness(t *testing.T) *harness {
	t.Helper()
	dsn := os.Getenv("ARTIFACT_REGISTRY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ARTIFACT_REGISTRY_TEST_POSTGRES_DSN is not set")
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	connection := dbcore.Config{Host: parsed.Host, Port: int(parsed.Port), User: parsed.User, Password: parsed.Password, Database: parsed.Database, SSLMode: "disable"}
	ctx := context.Background()
	admin, err := connection.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	roles := schema.Roles{Owner: "artifact_registry_owner", Migrator: "artifact_registry_migrator", Runtime: "artifact_registry_runtime", Maintenance: "artifact_registry_maintenance", Readiness: "artifact_registry_readiness"}
	bootstrapOnce.Do(func() {
		for _, role := range []string{roles.Owner, roles.Migrator, roles.Runtime, roles.Maintenance, roles.Readiness} {
			var exists bool
			if bootstrapError = admin.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); bootstrapError != nil {
				return
			}
			if !exists {
				kind := "LOGIN NOINHERIT PASSWORD 'test-only'"
				if role == roles.Owner {
					kind = "NOLOGIN NOINHERIT"
				}
				if _, bootstrapError = admin.Exec("CREATE ROLE " + dbcore.QuoteIdentifier(role) + " " + kind); bootstrapError != nil {
					return
				}
			}
		}
		_, bootstrapError = admin.Exec("GRANT " + roles.Owner + " TO " + roles.Migrator + " WITH INHERIT FALSE, SET TRUE")
	})
	if bootstrapError != nil {
		t.Fatal(bootstrapError)
	}
	name := "artreg_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err = admin.Exec("CREATE SCHEMA " + name + " AUTHORIZATION " + roles.Owner + "; GRANT USAGE ON SCHEMA " + name + " TO " + roles.Migrator); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + name + " CASCADE"); err != nil {
			t.Error(err)
		}
	})
	migrator := connection
	migrator.User = roles.Migrator
	migrator.Password = envOr("ARTIFACT_REGISTRY_TEST_MIGRATOR_PASSWORD", "test-only")
	target, err := schema.BuildTarget(migrator, name, &roles, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = dbcore.EnsureLatest(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err = dbcore.EnsureLatest(ctx, target); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	endpoint := os.Getenv("ARTIFACT_REGISTRY_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Fatal("ARTIFACT_REGISTRY_TEST_S3_ENDPOINT must be set for integration tests")
	}
	runtime := connection
	runtime.User = roles.Runtime
	runtime.Password = envOr("ARTIFACT_REGISTRY_TEST_RUNTIME_PASSWORD", "test-only")
	config := Config{DB: runtime, Schema: name, ObjectStore: ObjectStoreConfig{Endpoint: endpoint, AccessKey: envOr("ARTIFACT_REGISTRY_TEST_S3_ACCESS_KEY", "minioadmin"), SecretKey: envOr("ARTIFACT_REGISTRY_TEST_S3_SECRET_KEY", "minioadmin"), Bucket: "artifact-registry-testing", BasePrefix: name, RetryCount: 0}}
	clock := &fixedClock{time.Now().UTC().Truncate(time.Microsecond)}
	r, err := New(ctx, config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	maintenanceConfig := config
	maintenanceConfig.DB.User = roles.Maintenance
	maintenanceConfig.DB.Password = envOr("ARTIFACT_REGISTRY_TEST_MAINTENANCE_PASSWORD", "test-only")
	m, err := NewMaintenance(ctx, MaintenanceConfig{maintenanceConfig, CleanupSettings{StagedRetention: time.Hour, AbortedRetention: time.Hour, MaxBatchSize: 100, LockTimeout: 100 * time.Millisecond, CycleTimeout: time.Minute}}, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return &harness{admin, r, m, config, clock, roles}
}
func envOr(name, def string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return def
}
func (h *harness) diagnostic(t *testing.T, ref string) *DiagnosticInfo {
	t.Helper()
	var result *DiagnosticInfo
	err := h.r.RunTx(context.Background(), func(tx *txscope.Scope) error {
		var err error
		result, err = (&Advanced{h.r}).LookupPublicationDiagnostic(tx.Context(), tx, ref)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func (h *harness) publish(t *testing.T, payload []byte) *PreparedArtifact {
	t.Helper()
	ctx := context.Background()
	p, err := h.r.PrepareBytes(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.r.RunTx(ctx, func(tx *txscope.Scope) error { _, err := h.r.CommitPrepared(tx.Context(), tx, p); return err }); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIntegrationPublishReadDiscard(t *testing.T) {
	h := integrationHarness(t)
	h.clock.value = h.clock.value.Add(123 * time.Nanosecond)
	ctx := context.Background()
	payload := []byte("artifact-registry互換性\x00\xff")
	p := h.publish(t, payload)
	var artifact *CommittedArtifact
	err := h.r.RunTx(ctx, func(tx *txscope.Scope) error {
		var err error
		artifact, err = h.r.LookupCommittedPublication(tx.Context(), tx, p.Ref())
		if err != nil {
			return err
		}
		if _, err = h.r.ReadBytes(tx.Context(), artifact); !errors.Is(err, txscope.ErrNotSucceeded) {
			t.Fatalf("open lookup read: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := h.r.ReadBytes(ctx, artifact)
	if !artifact.Info().UploadedAt.Equal(p.Info().UploadedAt) {
		t.Fatal("prepared and stored upload timestamps differ")
	}
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatal(data, err)
	}
	data, err = h.r.ReadRangeBytes(ctx, artifact, 1, 5)
	if err != nil || !bytes.Equal(data, payload[1:6]) {
		t.Fatal(data, err)
	}
	for _, bounds := range [][2]int64{{-1, 1}, {0, 0}, {int64(len(payload)), 1}, {1, int64(len(payload))}, {1, 1 << 62}} {
		if _, err = h.r.ReadRangeBytes(ctx, artifact, bounds[0], bounds[1]); err == nil {
			t.Fatal("invalid range accepted", bounds)
		}
	}
	copy := *artifact
	if _, err = h.r.ReadBytes(ctx, &copy); err == nil {
		t.Fatal("copied committed handle accepted")
	}
	preparedCopy := *p
	if err = h.r.AbortPrepared(ctx, &preparedCopy); err == nil {
		t.Fatal("copied prepared handle accepted")
	}
	if err = h.r.AbortPrepared(ctx, p); err != nil {
		t.Fatalf("committed abort noop: %v", err)
	}
	err = h.r.RunTx(ctx, func(tx *txscope.Scope) error {
		results, err := h.r.RequestDiscardMany(tx.Context(), tx, []string{p.Ref(), p.Ref(), uuid.NewString()})
		if err != nil {
			return err
		}
		if results[0].Status != "discard_requested" || results[1].Status != results[0].Status || results[2].Status != "not_found" {
			t.Fatal(results)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if d := h.diagnostic(t, p.Ref()); d.PublicationStatus != Discarding {
		t.Fatal(d)
	}
	summary, err := h.m.RunCleanupCycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.CleanedPublicationCount != 1 || summary.DeletedObjectCount != 1 || summary.Status != "completed" {
		t.Fatal(summary)
	}
	d := h.diagnostic(t, p.Ref())
	if d.PublicationStatus != Discarded || d.ObjectStatus != Deleted {
		t.Fatal(d)
	}
	err = h.r.RunTx(ctx, func(tx *txscope.Scope) error {
		got, err := h.r.LookupCommittedPublication(tx.Context(), tx, p.Ref())
		if err != nil {
			return err
		}
		if got != nil {
			t.Fatal("discarded artifact was readable")
		}
		results, err := h.r.RequestDiscardMany(tx.Context(), tx, []string{p.Ref()})
		if err == nil && (!results[0].Idempotent() || results[0].Status != "already_discarded") {
			t.Fatal(results)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationRollbackAndBatchAtomicity(t *testing.T) {
	h := integrationHarness(t)
	ctx := context.Background()
	items, err := h.r.PrepareManyBytes(ctx, [][]byte{[]byte("one"), []byte("two")})
	if err != nil {
		t.Fatal(err)
	}
	primary := errors.New("domain transaction failed")
	err = h.r.RunTx(ctx, func(tx *txscope.Scope) error {
		if _, err := h.r.CommitPreparedMany(tx.Context(), tx, items); err != nil {
			return err
		}
		return primary
	})
	if !errors.Is(err, primary) {
		t.Fatal(err)
	}
	for _, p := range items {
		d := h.diagnostic(t, p.Ref())
		if d.PublicationStatus != Aborted || d.ObjectStatus != Deleted {
			t.Fatal(d)
		}
	}
	items, err = h.r.PrepareManyBytes(ctx, [][]byte{[]byte("three"), []byte("four")})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.r.AbortPrepared(ctx, items[1]); err != nil {
		t.Fatal(err)
	}
	err = h.r.RunTx(ctx, func(tx *txscope.Scope) error {
		if _, err := h.r.CommitPreparedMany(tx.Context(), tx, items); err == nil {
			t.Fatal("invalid batch committed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if d := h.diagnostic(t, items[0].Ref()); d.PublicationStatus != Staged {
		t.Fatal("partial batch commit", d)
	}
	if err = h.r.AbortPrepared(ctx, items[0]); err != nil {
		t.Fatal(err)
	}
	if err = h.r.AbortPreparedMany(ctx, nil); err != nil {
		t.Fatal(err)
	}
}

type faultStore struct {
	blobStore
	putCount         int
	failPut          int
	failDelete       bool
	started, release chan struct{}
}

func (s *faultStore) Put(ctx context.Context, path string, data []byte) error {
	s.putCount++
	if s.started != nil && s.putCount == 2 {
		close(s.started)
		<-s.release
	}
	if s.putCount == s.failPut {
		return errors.New("injected upload failure")
	}
	return s.blobStore.Put(ctx, path, data)
}
func (s *faultStore) Delete(ctx context.Context, path string) error {
	if s.failDelete {
		return errors.New("injected delete failure")
	}
	return s.blobStore.Delete(ctx, path)
}

func TestIntegrationUploadFailureAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelled), func(t *testing.T) {
			h := integrationHarness(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := &faultStore{blobStore: h.r.store, failPut: 2}
			h.r.store = store
			if cancelled {
				store.failPut = 0
				store.started = make(chan struct{})
				store.release = make(chan struct{})
				go func() { <-store.started; cancel(); close(store.release) }()
			}
			items, err := h.r.PrepareManyBytes(ctx, [][]byte{[]byte("one"), []byte("two"), []byte("three")})
			if err == nil || items != nil {
				t.Fatal(items, err)
			}
			if cancelled && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			var count int
			if err = h.admin.QueryRow("SELECT COUNT(*) FROM " + h.r.table("artifact_registry_publications") + " WHERE status='aborted'").Scan(&count); err != nil || count != 3 {
				t.Fatal(count, err)
			}
			if store.putCount != 2 {
				t.Fatal("batch was not fail-fast", store.putCount)
			}
			h.clock.value = h.clock.value.Add(2 * time.Hour)
			summary, err := h.m.RunCleanupCycle(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if summary.RemainingBacklog.Aborted.EligibleCount != 0 {
				t.Fatal(summary)
			}
		})
	}
}

func TestIntegrationCleanupFailuresLocksAndStale(t *testing.T) {
	h := integrationHarness(t)
	ctx := context.Background()
	p := h.publish(t, []byte("discard"))
	if err := h.r.RunTx(ctx, func(tx *txscope.Scope) error {
		_, err := h.r.RequestDiscardMany(tx.Context(), tx, []string{p.Ref()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	store := &faultStore{blobStore: h.m.registry.store, failDelete: true}
	h.m.registry.store = store
	summary, err := h.m.RunCleanupCycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.DeleteFailedCount < 1 || summary.Status != "retryable" || summary.RemainingBacklog.DeleteFailed.EligibleCount != 1 {
		t.Fatal(summary)
	}
	store.failDelete = false
	summary, err = h.m.RunCleanupCycle(ctx)
	if err != nil || summary.DeletedObjectCount != 1 || summary.RemainingBacklog.DeleteFailed.EligibleCount != 0 {
		t.Fatal(summary, err)
	}
	stale, err := h.r.PrepareBytes(ctx, []byte("stale"))
	if err != nil {
		t.Fatal(err)
	}
	h.clock.value = h.clock.value.Add(2 * time.Hour)
	summary, err = h.m.RunCleanupCycle(ctx)
	if err != nil || summary.StaleStagedResult.CleanedPublicationCount != 1 {
		t.Fatal(summary, err)
	}
	if h.diagnostic(t, stale.Ref()).PublicationStatus != Aborted {
		t.Fatal("stale not aborted")
	}
	lock, err := dbcore.AcquireLock(ctx, h.admin, "artifact-registry/cleanup/"+h.config.Schema, false, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.m.RunCleanupCycle(ctx)
	if !errors.Is(err, dbcore.ErrLockTimeout) {
		t.Fatal("cleanup lock not enforced", err)
	}
	if err = lock.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationPermissionsAndReadiness(t *testing.T) {
	h := integrationHarness(t)
	ctx := context.Background()
	for _, db := range []*sql.DB{h.r.db, h.m.registry.db} {
		for _, statement := range []string{"UPDATE " + h.r.table("artifact_registry_objects") + " SET object_path='x' WHERE FALSE", "DELETE FROM " + h.r.table("artifact_registry_publications") + " WHERE FALSE"} {
			_, err := db.ExecContext(ctx, statement)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
				t.Fatal("direct DML not rejected", err)
			}
		}
	}
	readiness := h.config.DB
	readiness.User = h.roles.Readiness
	readiness.Password = envOr("ARTIFACT_REGISTRY_TEST_READINESS_PASSWORD", "test-only")
	db, err := readiness.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = dbcore.EnsureExpected(ctx, db, h.config.Schema, schema.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("SELECT artifact_ref FROM " + h.r.table("artifact_registry_publications")); err == nil {
		t.Fatal("readiness role read lifecycle table")
	}
	lock, err := dbcore.AcquireLock(ctx, h.admin, "artifact-registry/"+h.config.Schema, true, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dbcore.AcquireLock(ctx, h.admin, "artifact-registry/"+h.config.Schema, false, 50*time.Millisecond); !errors.Is(err, dbcore.ErrLockTimeout) {
		t.Fatal(err)
	}
	lock.Release()
	if _, err = h.admin.Exec("UPDATE " + h.r.table("schema_migrations") + " SET version_id=999 WHERE version_id=2"); err != nil {
		t.Fatal(err)
	}
	if _, err = h.r.PrepareBytes(ctx, []byte("bad schema")); !errors.Is(err, dbcore.ErrSchemaVersion) {
		t.Fatal(err)
	}
}

func TestIntegrationIntegrityAndInvalidInputs(t *testing.T) {
	h := integrationHarness(t)
	ctx := context.Background()
	p := h.publish(t, []byte("original"))
	var artifact *CommittedArtifact
	if err := h.r.RunTx(ctx, func(tx *txscope.Scope) error {
		var err error
		artifact, err = h.r.LookupCommittedPublication(tx.Context(), tx, p.Ref())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	path := h.diagnostic(t, p.Ref()).ObjectPath
	for _, test := range []struct{ payload, reason string }{{"short", "size_mismatch"}, {"modified", "digest_mismatch"}} {
		if err := h.r.store.Put(ctx, path, []byte(test.payload)); err != nil {
			t.Fatal(err)
		}
		_, err := h.r.ReadBytes(ctx, artifact)
		var integrity *IntegrityError
		if !errors.As(err, &integrity) || integrity.Reason != test.reason {
			t.Fatal(err)
		}
	}
	if err := h.r.RunTx(ctx, func(tx *txscope.Scope) error {
		if got, err := h.r.LookupCommittedPublication(tx.Context(), tx, "bad"); got != nil || err != nil {
			t.Fatal(got, err)
		}
		if _, err := h.r.RequestDiscardMany(tx.Context(), tx, []string{p.Ref(), "bad"}); err == nil {
			t.Fatal("invalid discard accepted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := h.diagnostic(t, p.Ref()); got.PublicationStatus != Committed {
		t.Fatal("validation mutated DB", got)
	}
	if _, err := h.r.PrepareBytes(ctx, nil); err != nil {
		t.Fatal("empty payload", err)
	}
}

func TestIntegrationDomainTransaction(t *testing.T) {
	h := integrationHarness(t)
	ctx := context.Background()
	table := h.r.table("domain_rows")
	if _, err := h.admin.Exec("CREATE TABLE " + table + "(ref text); GRANT INSERT,SELECT ON " + table + " TO " + h.roles.Runtime); err != nil {
		t.Fatal(err)
	}
	p, err := h.r.PrepareBytes(ctx, []byte("domain"))
	if err != nil {
		t.Fatal(err)
	}
	primary := fmt.Errorf("domain failure")
	err = h.r.RunTx(ctx, func(tx *txscope.Scope) error {
		if _, err := tx.ExecContext(tx.Context(), "INSERT INTO "+table+" VALUES($1)", p.Ref()); err != nil {
			return err
		}
		if _, err := h.r.CommitPrepared(tx.Context(), tx, p); err != nil {
			return err
		}
		return primary
	})
	if !errors.Is(err, primary) {
		t.Fatal(err)
	}
	var count int
	if err = h.admin.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if h.diagnostic(t, p.Ref()).PublicationStatus != Aborted {
		t.Fatal("publication not compensated")
	}
}

func TestIntegrationMigrationPreflightAndResults(t *testing.T) {
	h := integrationHarness(t)
	ctx := context.Background()
	connection := h.config.DB
	connection.User = h.roles.Migrator
	connection.Password = envOr("ARTIFACT_REGISTRY_TEST_MIGRATOR_PASSWORD", "test-only")
	target, err := schema.BuildTarget(connection, h.config.Schema, &h.roles, false)
	if err != nil {
		t.Fatal(err)
	}
	step, err := dbcore.Migrate(ctx, target)
	if err != nil || step.Status != "ok" || step.CurrentRevision == nil || *step.CurrentRevision != "000000000002" || step.Action == nil || *step.Action != "upgrade" {
		t.Fatal(step, err)
	}
	for _, legacy := range []bool{false, true} {
		name := h.config.Schema + "_unmanaged"
		if legacy {
			name = h.config.Schema + "_legacy"
		}
		if _, err = h.admin.Exec("CREATE SCHEMA " + name + " AUTHORIZATION " + h.roles.Owner + "; GRANT USAGE ON SCHEMA " + name + " TO " + h.roles.Migrator); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { h.admin.Exec("DROP SCHEMA " + name + " CASCADE") })
		owner, err := connection.Open(ctx, h.roles.Owner)
		if err != nil {
			t.Fatal(err)
		}
		table := "unmanaged"
		if legacy {
			table = "alembic_version"
		}
		_, err = owner.Exec("CREATE TABLE " + name + "." + table + "(value text)")
		owner.Close()
		if err != nil {
			t.Fatal(err)
		}
		target, err = schema.BuildTarget(connection, name, &h.roles, false)
		if err != nil {
			t.Fatal(err)
		}
		step, err = dbcore.Migrate(ctx, target)
		if err == nil || step.Status != "error" || step.Message == nil || step.Action != nil {
			t.Fatal(step, err)
		}
		var marker bool
		if err = h.admin.QueryRow("SELECT to_regclass($1) IS NOT NULL", name+".schema_migrations").Scan(&marker); err != nil || marker {
			t.Fatal("unmanaged schema was adopted", marker, err)
		}
	}
}

func TestIntegrationExternalIOAndAttachedMaintenance(t *testing.T) {
	h := integrationHarness(t)
	ctx := context.Background()
	var before int
	if err := h.admin.QueryRow("SELECT count(*) FROM " + h.r.table("artifact_registry_publications")).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := h.r.RunTx(ctx, func(tx *txscope.Scope) error {
		if _, err := h.r.PrepareBytes(tx.Context(), []byte("forbidden")); !errors.Is(err, txscope.ErrExternalIO) {
			t.Fatal(err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := h.admin.QueryRow("SELECT count(*) FROM " + h.r.table("artifact_registry_publications")).Scan(&after); err != nil || before != after {
		t.Fatal("I/O rejection left intent", before, after, err)
	}
	var prepared *PreparedArtifact
	if err := h.r.RunTx(ctx, func(tx *txscope.Scope) error {
		return txscope.ExternalIOInTx(tx.Context(), "advanced publish with durable reconciliation", func(authorized context.Context) error {
			var err error
			prepared, err = h.r.PrepareBytes(authorized, []byte("advanced"))
			if err != nil {
				return err
			}
			_, err = h.r.CommitPrepared(authorized, tx, prepared)
			return err
		})
	}); err != nil {
		t.Fatal(err)
	}
	if d := h.diagnostic(t, prepared.Ref()); d.PublicationStatus != Committed {
		t.Fatal(d)
	}
	config := MaintenanceConfig{Runtime: h.config, Cleanup: h.m.settings}
	config.Runtime.DB = dbcore.Config{Host: "unreachable"}
	attached, err := AttachMaintenance(h.m.registry.db, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = attached.RunCleanupCycle(ctx); err != nil {
		t.Fatal("attached pool was not used", err)
	}
	if err = attached.Close(); err != nil {
		t.Fatal(err)
	}
	if err = h.m.registry.db.PingContext(ctx); err != nil {
		t.Fatal("caller-owned pool was closed", err)
	}
}
