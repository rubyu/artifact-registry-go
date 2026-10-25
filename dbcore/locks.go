package dbcore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/blake2b"
)

func LockID(key string) int64 {
	hash, _ := blake2b.New(8, nil)
	hash.Write([]byte(key))
	return int64(binary.BigEndian.Uint64(hash.Sum(nil)))
}

type Lock struct {
	conn   *sql.Conn
	id     int64
	shared bool
	extras []int64
}

// AcquireLock は専用 connection に session lock を保持する。
func AcquireLock(ctx context.Context, db *sql.DB, key string, shared bool, timeout time.Duration) (*Lock, error) {
	if key == "" || timeout <= 0 {
		return nil, errors.New("lock key and positive timeout are required")
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := db.Conn(waitCtx)
	if err != nil {
		return nil, lockError(waitCtx, err)
	}
	id := LockID(key)
	query := "SELECT pg_try_advisory_lock($1)"
	if shared {
		query = "SELECT pg_try_advisory_lock_shared($1)"
	}
	for {
		var acquired bool
		err = conn.QueryRowContext(waitCtx, query, id).Scan(&acquired)
		if err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			conn.Close()
			return nil, lockError(waitCtx, err)
		}
		if acquired {
			return &Lock{conn: conn, id: id, shared: shared}, nil
		}
		select {
		case <-waitCtx.Done():
			conn.Close()
			return nil, errors.Join(ErrLockTimeout, waitCtx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (l *Lock) Release() error {
	if l == nil || l.conn == nil {
		return nil
	}
	conn := l.conn
	l.conn = nil
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := len(l.extras) - 1; i >= 0; i-- {
		var released bool
		err := conn.QueryRowContext(ctx, "SELECT pg_advisory_unlock_shared($1)", l.extras[i]).Scan(&released)
		if err != nil || !released {
			conn.Raw(func(any) error { return driver.ErrBadConn })
			return errors.Join(errors.New("shared advisory unlock failed"), err, conn.Close())
		}
	}
	query := "SELECT pg_advisory_unlock($1)"
	if l.shared {
		query = "SELECT pg_advisory_unlock_shared($1)"
	}
	var released bool
	err := conn.QueryRowContext(ctx, query, l.id).Scan(&released)
	if err != nil || !released {
		// unlock を確認できない session を pool に戻さない。
		conn.Raw(func(any) error { return driver.ErrBadConn })
		return errors.Join(errors.New("advisory unlock failed"), err, conn.Close())
	}
	return conn.Close()
}

// AddShared は同じ connection に migration 競合防止用の lock を追加する。
func (l *Lock) AddShared(ctx context.Context, key string, timeout time.Duration) error {
	if l == nil || l.conn == nil || key == "" || timeout <= 0 {
		return errors.New("active lock, key and positive timeout are required")
	}
	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	id := LockID(key)
	for {
		var acquired bool
		if err := l.conn.QueryRowContext(wait, "SELECT pg_try_advisory_lock_shared($1)", id).Scan(&acquired); err != nil {
			_ = l.conn.Raw(func(any) error { return driver.ErrBadConn })
			return lockError(wait, err)
		}
		if acquired {
			l.extras = append(l.extras, id)
			return nil
		}
		select {
		case <-wait.Done():
			return errors.Join(ErrLockTimeout, wait.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func lockError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return errors.Join(ErrLockTimeout, ctx.Err(), err)
	}
	return err
}

func EnsureExpected(ctx context.Context, db *sql.DB, schema string, expected int64) error {
	if err := ValidateIdentifier(schema); err != nil {
		return err
	}
	var version int64
	err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_id),0) FROM "+QuoteIdentifier(schema)+".schema_migrations WHERE is_applied").Scan(&version)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version != expected {
		return fmt.Errorf("%w: expected %d, got %d", ErrSchemaVersion, expected, version)
	}
	return nil
}

func WithSchemaReady(ctx context.Context, db *sql.DB, schema, key string, version int64, timeout time.Duration, fn func(context.Context) error) (err error) {
	lock, err := AcquireLock(ctx, db, key, true, timeout)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Release()) }()
	if err = EnsureExpected(ctx, db, schema, version); err != nil {
		return err
	}
	return fn(ctx)
}
