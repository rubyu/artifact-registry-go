package dbcore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
)

type MigrationTarget struct {
	Name            string
	Connection      Config
	Schema          string
	LockKey         string
	MigrationRole   string
	CreateSchema    bool
	ExpectedVersion int64
	Migrations      []*goose.Migration
}

func EnsureLatest(ctx context.Context, target MigrationTarget) error {
	_, err := Migrate(ctx, target)
	return err
}

func Migrate(ctx context.Context, target MigrationTarget) (result MigrationStepResult, err error) {
	name := target.Name
	if name == "" {
		name = target.Schema
	}
	revision := fmt.Sprintf("%012d", target.ExpectedVersion)
	result = MigrationStepResult{TargetName: name, Status: "error", SchemaName: target.Schema, TargetRevision: &revision}
	err = ensureLatest(ctx, target, &result)
	if err != nil {
		message := err.Error()
		result.Message = &message
		return result, &MigrationError{Err: err, Result: result}
	}
	result.Status = "ok"
	return result, nil
}

func ensureLatest(ctx context.Context, target MigrationTarget, result *MigrationStepResult) (err error) {
	if err = ValidateSchemaName(target.Schema); err != nil {
		return err
	}
	if target.LockKey == "" || target.ExpectedVersion <= 0 {
		return errors.New("migration target requires lock key and expected version")
	}
	var latest int64
	for _, migration := range target.Migrations {
		if migration == nil {
			return errors.New("nil migration")
		}
		if migration.Version > latest {
			latest = migration.Version
		}
	}
	if latest != target.ExpectedVersion {
		return errors.New("expected migration version must equal the local migration head")
	}
	db, err := target.Connection.Open(ctx, target.MigrationRole)
	if err != nil {
		return err
	}
	defer db.Close()
	lock, err := AcquireLock(ctx, db, target.LockKey, false, 30*time.Second)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Release()) }()
	if target.CreateSchema {
		if _, err = db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+QuoteIdentifier(target.Schema)); err != nil {
			return err
		}
	}
	var owner string
	if err = db.QueryRowContext(ctx, "SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname=$1", target.Schema).Scan(&owner); err != nil {
		return fmt.Errorf("schema must be bootstrapped before migration: %w", err)
	}
	if target.MigrationRole != "" && owner != target.MigrationRole {
		return errors.New("schema owner does not match migration role")
	}
	// Alembic marker の自動付け替えは行わない。
	var legacy bool
	if err = db.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", QuoteIdentifier(target.Schema)+".alembic_version").Scan(&legacy); err != nil {
		return err
	}
	if legacy {
		return errors.New("Alembic-managed database requires an explicit reviewed cutover; automatic adoption is disabled")
	}
	var marker bool
	if err = db.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", QuoteIdentifier(target.Schema)+".schema_migrations").Scan(&marker); err != nil {
		return err
	}
	if marker {
		version, e := SchemaVersion(ctx, db, target.Schema)
		if e != nil {
			return e
		}
		revision := fmt.Sprintf("%012d", version)
		result.CurrentRevision = &revision
	} else {
		var nonempty bool
		if err = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_class WHERE relnamespace=to_regnamespace($1)) OR EXISTS(SELECT 1 FROM pg_proc WHERE pronamespace=to_regnamespace($1)) OR EXISTS(SELECT 1 FROM pg_type WHERE typnamespace=to_regnamespace($1))", target.Schema).Scan(&nonempty); err != nil {
			return err
		}
		if nonempty {
			return errors.New("nonempty schema without a Go migration marker requires an explicit reviewed cutover")
		}
	}
	store, err := database.NewStore(database.DialectPostgres, QuoteIdentifier(target.Schema)+".schema_migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider("", db, nil, goose.WithStore(quotedStore{store}), goose.WithDisableGlobalRegistry(true), goose.WithGoMigrations(target.Migrations...))
	if err != nil {
		return err
	}
	action := "upgrade"
	result.Action = &action
	if _, err = provider.Up(ctx); err != nil {
		return fmt.Errorf("schema migration: %w", err)
	}
	version, err := SchemaVersion(ctx, db, target.Schema)
	if err != nil {
		return err
	}
	revision := fmt.Sprintf("%012d", version)
	result.CurrentRevision = &revision
	if version != target.ExpectedVersion {
		return fmt.Errorf("%w: expected %d, got %d", ErrSchemaVersion, target.ExpectedVersion, version)
	}
	return nil
}

// Goose 標準の存在確認は引用符付き schema 名を解析しないため、名前を parameter として渡す。
type quotedStore struct{ database.Store }

func (s quotedStore) TableExists(ctx context.Context, db database.DBTxConn) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", s.Tablename()).Scan(&exists)
	return exists, err
}

func SchemaVersion(ctx context.Context, db *sql.DB, schema string) (int64, error) {
	if err := ValidateSchemaName(schema); err != nil {
		return 0, err
	}
	var version int64
	err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_id),0) FROM "+QuoteIdentifier(schema)+".schema_migrations WHERE is_applied").Scan(&version)
	return version, err
}
