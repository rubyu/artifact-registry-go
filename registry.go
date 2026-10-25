package artifactregistry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rubyu/artifact-registry-go/dbcore"
	"github.com/rubyu/artifact-registry-go/internal/objectstore"
	"github.com/rubyu/artifact-registry-go/schema"
	"github.com/rubyu/artifact-registry-go/txscope"
)

type blobStore interface {
	Prefix() string
	Put(context.Context, string, []byte) error
	Get(context.Context, string, int64, int64) ([]byte, error)
	Delete(context.Context, string) error
}
type Registry struct {
	db          *sql.DB
	store       blobStore
	schema      string
	clock       Clock
	ownsDB      bool
	closeStore  func()
	maintenance bool
}

func New(ctx context.Context, config Config, opts ...Option) (*Registry, error) {
	db, err := config.DB.Open(ctx, "")
	if err != nil {
		return nil, err
	}
	r, err := Attach(db, config, opts...)
	if err != nil {
		db.Close()
		return nil, err
	}
	r.ownsDB = true
	return r, nil
}

// Attach は呼び出し元が所有する接続 pool を閉じない。
func Attach(db *sql.DB, config Config, opts ...Option) (*Registry, error) {
	if db == nil {
		return nil, errors.New("database pool is required")
	}
	name := config.Schema
	if name == "" {
		name = "artifact_registry"
	}
	if err := dbcore.ValidateIdentifier(name); err != nil {
		return nil, err
	}
	c := config.ObjectStore
	store, err := objectstore.New(objectstore.Config{Endpoint: c.Endpoint, AccessKey: c.AccessKey, SecretKey: c.SecretKey, Bucket: c.Bucket, BasePrefix: c.BasePrefix, Secure: c.Secure, ConnectTimeout: c.ConnectTimeout, ReadTimeout: c.ReadTimeout, RetryCount: c.RetryCount, Credentials: c.Credentials})
	if err != nil {
		return nil, err
	}
	o := options{clock: systemClock{}}
	for _, option := range opts {
		option(&o)
	}
	if o.clock == nil {
		store.Close()
		return nil, errors.New("clock is required")
	}
	return &Registry{db: db, store: store, schema: name, clock: o.clock, closeStore: store.Close}, nil
}
func (r *Registry) Close() error {
	if r.closeStore != nil {
		r.closeStore()
	}
	if r.ownsDB {
		return r.db.Close()
	}
	return nil
}
func (r *Registry) RunTx(ctx context.Context, fn func(*txscope.Scope) error) error {
	return txscope.Run(ctx, r.db, func(tx *txscope.Scope) error {
		if err := r.guardTx(tx.Context(), tx); err != nil {
			return err
		}
		return fn(tx)
	})
}
func (r *Registry) table(name string) string { return dbcore.QuoteIdentifier(r.schema) + "." + name }
func (r *Registry) call(ctx context.Context, tx *txscope.Scope, name string, args ...any) (bool, error) {
	if err := tx.RequireOpen(); err != nil {
		return false, err
	}
	params := make([]string, len(args))
	for i := range args {
		params[i] = fmt.Sprintf("$%d", i+1)
	}
	var changed bool
	err := tx.QueryRowContext(ctx, "SELECT "+r.table("artifact_registry_"+name)+"("+strings.Join(params, ",")+")", args...).Scan(&changed)
	return changed, err
}
func (r *Registry) guardTx(ctx context.Context, tx *txscope.Scope) error {
	if tx == nil {
		return errors.New("transaction scope is required")
	}
	if err := tx.RequireOpen(); err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		var acquired bool
		if err := tx.QueryRowContext(wait, "SELECT pg_try_advisory_xact_lock_shared($1)", dbcore.LockID("artifact-registry/"+r.schema)).Scan(&acquired); err != nil {
			return err
		}
		if acquired {
			break
		}
		select {
		case <-wait.Done():
			return errors.Join(dbcore.ErrLockTimeout, wait.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if r.maintenance {
		return nil
	}
	var version int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_id),0) FROM "+r.table("schema_migrations")+" WHERE is_applied").Scan(&version); err != nil {
		return err
	}
	if version != schema.Version {
		return fmt.Errorf("%w: expected %d, got %d", dbcore.ErrSchemaVersion, schema.Version, version)
	}
	return nil
}
func (r *Registry) ioContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
}
func (r *Registry) mutation(ctx context.Context, fn func(*txscope.Scope) error) error {
	return r.RunTx(ctx, fn)
}
