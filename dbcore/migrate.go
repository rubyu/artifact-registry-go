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
	Connection      Config
	Schema          string
	LockKey         string
	MigrationRole   string
	CreateSchema    bool
	ExpectedVersion int64
	Migrations      []*goose.Migration
}

func EnsureLatest(ctx context.Context, target MigrationTarget) (err error) {
	if err = ValidateIdentifier(target.Schema); err != nil {
		return err
	}
	if target.LockKey == "" || target.ExpectedVersion <= 0 {
		return errors.New("migration target requires lock key and expected version")
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
	// Alembic marker の自動付け替えは行わない。
	var legacy bool
	if err = db.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", QuoteIdentifier(target.Schema)+".alembic_version").Scan(&legacy); err != nil {
		return err
	}
	if legacy {
		return errors.New("Alembic-managed database requires an explicit reviewed cutover; automatic adoption is disabled")
	}
	store, err := database.NewStore(database.DialectPostgres, QuoteIdentifier(target.Schema)+".schema_migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider("", db, nil, goose.WithStore(quotedStore{store}), goose.WithDisableGlobalRegistry(true), goose.WithGoMigrations(target.Migrations...))
	if err != nil {
		return err
	}
	if _, err = provider.Up(ctx); err != nil {
		return fmt.Errorf("schema migration: %w", err)
	}
	return EnsureExpected(ctx, db, target.Schema, target.ExpectedVersion)
}

// Goose 標準の存在確認は引用符付き schema 名を解析しないため、名前を parameter として渡す。
type quotedStore struct{ database.Store }

func (s quotedStore) TableExists(ctx context.Context, db database.DBTxConn) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", s.Tablename()).Scan(&exists)
	return exists, err
}

func SchemaVersion(ctx context.Context, db *sql.DB, schema string) (int64, error) {
	if err := ValidateIdentifier(schema); err != nil {
		return 0, err
	}
	var version int64
	err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_id),0) FROM "+QuoteIdentifier(schema)+".schema_migrations WHERE is_applied").Scan(&version)
	return version, err
}
