package artifactregistry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rubyu/artifact-registry-go/dbcore"
	"github.com/rubyu/artifact-registry-go/txscope"
)

type PreparedArtifact struct {
	info            Artifact
	objectRef, path string
	owner           *Registry
	self            *PreparedArtifact
}

func (p *PreparedArtifact) Info() Artifact {
	if p == nil {
		return Artifact{}
	}
	return p.info
}
func (p *PreparedArtifact) Ref() string { return p.Info().Ref }
func (r *Registry) prepared(p *PreparedArtifact) error {
	if p == nil || p.self != p || p.owner != r {
		return errors.New("prepared artifact was not created by this registry")
	}
	return nil
}

type pendingUpload struct {
	prepared *PreparedArtifact
	payload  []byte
	uploaded bool
}

func (r *Registry) PrepareBytes(ctx context.Context, payload []byte) (*PreparedArtifact, error) {
	items, err := r.PrepareManyBytes(ctx, [][]byte{payload})
	if err != nil {
		return nil, err
	}
	return items[0], nil
}
func (r *Registry) PrepareManyBytes(ctx context.Context, items [][]byte) ([]*PreparedArtifact, error) {
	if len(items) == 0 {
		return nil, errors.New("items must not be empty")
	}
	if err := txscope.AssertExternalIOAllowed(ctx); err != nil {
		return nil, &PreparationError{"artifact_upload", err}
	}
	now := r.now()
	var uploads []*pendingUpload
	for attempt := 0; attempt < 3; attempt++ {
		uploads = nil
		var refs []string
		err := r.mutation(ctx, func(tx *txscope.Scope) error {
			var e error
			refs, e = dbcore.AllocateUUID7Many(tx.Context(), tx, len(items)*2)
			return e
		})
		if err == nil {
			for i, item := range items {
				p := &PreparedArtifact{info: Artifact{Ref: refs[i*2], DigestValue: dbcore.SHA256DigestValue(item), SizeBytes: int64(len(item)), UploadedAt: now}, objectRef: refs[i*2+1], owner: r}
				p.self = p
				p.path = strings.Trim(r.store.Prefix(), "/")
				if p.path != "" {
					p.path += "/"
				}
				p.path += now.Format("2006/01/02") + "/" + p.info.Ref
				err = r.mutation(ctx, func(tx *txscope.Scope) error {
					var ref, objectRef string
					return tx.QueryRowContext(tx.Context(), "SELECT artifact_ref::text,artifact_object_ref::text FROM "+r.table("artifact_registry_begin_publication")+"($1,$2,$3,$4,$5)", p.info.Ref, p.objectRef, p.path, p.info.DigestValue, now).Scan(&ref, &objectRef)
				})
				if err != nil {
					break
				}
				uploads = append(uploads, &pendingUpload{prepared: p, payload: append([]byte(nil), item...)})
			}
		}
		if err == nil {
			break
		}
		cleanup, cancel := r.ioContext(ctx)
		compensation := r.abortUploads(cleanup, uploads, now)
		cancel()
		var pgerr *pgconn.PgError
		retry := errors.As(err, &pgerr) && pgerr.Code == "23505" && (pgerr.ConstraintName == "artifact_registry_publications_artifact_ref_key" || pgerr.ConstraintName == "artifact_registry_objects_artifact_object_ref_key" || pgerr.ConstraintName == "artifact_registry_objects_object_path_key")
		if ctx.Err() != nil {
			return nil, errors.Join(ctx.Err(), compensation)
		}
		if !retry || attempt == 2 || compensation != nil {
			return nil, &PreparationError{"publication_intent", errors.Join(err, compensation)}
		}
	}
	var uploadErr error
	for _, upload := range uploads {
		if uploadErr = ctx.Err(); uploadErr != nil {
			break
		}
		operation, cancel := r.ioContext(ctx)
		uploadErr = r.store.Put(operation, upload.prepared.path, upload.payload)
		cancel()
		if uploadErr != nil {
			break
		}
		upload.uploaded = true
	}
	if uploadErr == nil {
		uploadErr = ctx.Err()
	}
	if uploadErr == nil {
		uploadErr = r.mutation(ctx, func(tx *txscope.Scope) error {
			for _, upload := range uploads {
				ok, err := r.call(tx.Context(), tx, "confirm_object_upload", upload.prepared.objectRef, upload.prepared.info.SizeBytes, now)
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("object upload confirmation rejected")
				}
			}
			return nil
		})
	}
	if uploadErr != nil {
		cleanup, cancel := r.ioContext(ctx)
		compensation := r.abortUploads(cleanup, uploads, now)
		cancel()
		if ctx.Err() != nil {
			return nil, errors.Join(ctx.Err(), compensation)
		}
		return nil, &PreparationError{"artifact_upload", errors.Join(uploadErr, compensation)}
	}
	prepared := make([]*PreparedArtifact, len(uploads))
	for i, upload := range uploads {
		prepared[i] = upload.prepared
	}
	return prepared, nil
}

func (r *Registry) CommitPrepared(ctx context.Context, tx *txscope.Scope, p *PreparedArtifact) (Artifact, error) {
	items, err := r.CommitPreparedMany(ctx, tx, []*PreparedArtifact{p})
	if err != nil {
		return Artifact{}, err
	}
	return items[0], nil
}
func (r *Registry) CommitPreparedMany(ctx context.Context, tx *txscope.Scope, items []*PreparedArtifact) ([]Artifact, error) {
	if len(items) == 0 {
		return nil, errors.New("prepared must not be empty")
	}
	for _, p := range items {
		if err := r.prepared(p); err != nil {
			return nil, err
		}
	}
	if err := r.guardTx(ctx, tx); err != nil {
		return nil, err
	}
	current := r.now()
	for _, p := range items {
		p := p
		if err := tx.OnFailed(func(cleanup context.Context) error {
			return txscope.ExternalIOInTx(cleanup, "artifact prepared abort after confirmed rollback", func(ctx context.Context) error { return r.abortPreparedAt(ctx, p, current) })
		}); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, "SAVEPOINT artifact_registry_publish"); err != nil {
		return nil, err
	}
	var result []Artifact
	for _, p := range items {
		ok, err := r.call(ctx, tx, "commit_publication", p.info.Ref, p.objectRef, current)
		if err == nil && !ok {
			err = errors.New("publication commit rejected")
		}
		if err != nil {
			_, rollbackErr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT artifact_registry_publish")
			_, releaseErr := tx.ExecContext(ctx, "RELEASE SAVEPOINT artifact_registry_publish")
			return nil, errors.Join(err, rollbackErr, releaseErr)
		}
		result = append(result, p.info)
	}
	_, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT artifact_registry_publish")
	return result, err
}

func (r *Registry) AbortPreparedMany(ctx context.Context, items []*PreparedArtifact) error {
	var errs []error
	current := r.now()
	for _, p := range items {
		if err := r.abortPreparedAt(ctx, p, current); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
func (r *Registry) AbortPrepared(ctx context.Context, p *PreparedArtifact) error {
	return r.abortPreparedAt(ctx, p, r.now())
}

func (r *Registry) abortPreparedAt(ctx context.Context, p *PreparedArtifact, now time.Time) error {
	if err := r.prepared(p); err != nil {
		return err
	}
	var aborted bool
	err := r.mutation(ctx, func(tx *txscope.Scope) error {
		var err error
		aborted, err = r.call(tx.Context(), tx, "abort_publication", p.info.Ref, p.objectRef, now)
		if err != nil {
			return err
		}
		if aborted {
			return nil
		}
		var status string
		err = tx.QueryRowContext(tx.Context(), "SELECT status::text FROM "+r.table("artifact_registry_publications")+" WHERE artifact_ref=$1 AND artifact_object_ref=$2", p.info.Ref, p.objectRef).Scan(&status)
		if err != nil {
			return err
		}
		switch status {
		case "committed", "discarding", "discarded", "aborted":
			return nil
		}
		return fmt.Errorf("publication abort rejected: %s", status)
	})
	if err != nil || !aborted {
		return err
	}
	return r.deleteAndReconcile(ctx, p.objectRef, p.path, "", now)
}
func (r *Registry) abortUploads(ctx context.Context, uploads []*pendingUpload, now time.Time) error {
	if len(uploads) == 0 {
		return nil
	}
	if err := r.mutation(ctx, func(tx *txscope.Scope) error {
		for _, upload := range uploads {
			p := upload.prepared
			if !upload.uploaded {
				if _, err := r.call(tx.Context(), tx, "fail_object_upload", p.objectRef); err != nil {
					return err
				}
			}
			if _, err := r.call(tx.Context(), tx, "abort_publication", p.info.Ref, p.objectRef, now); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	var errs []error
	for _, upload := range uploads {
		if upload.uploaded {
			if err := r.deleteAndReconcile(ctx, upload.prepared.objectRef, upload.prepared.path, "", now); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
func (r *Registry) deleteAndReconcile(ctx context.Context, objectRef, path, discardRef string, now time.Time) error {
	deleteErr := r.store.Delete(ctx, path)
	return r.mutation(ctx, func(tx *txscope.Scope) error {
		operation := "confirm_object_deleted"
		args := []any{objectRef, now}
		if deleteErr != nil {
			operation = "mark_object_delete_failed"
			args = []any{objectRef}
		}
		ok, err := r.call(tx.Context(), tx, operation, args...)
		if err != nil {
			return err
		}
		if !ok {
			var status string
			if err = tx.QueryRowContext(tx.Context(), "SELECT status::text FROM "+r.table("artifact_registry_objects")+" WHERE artifact_object_ref=$1", objectRef).Scan(&status); err != nil {
				return err
			}
			if status != "deleted" && status != "delete_failed" {
				return errors.New("object delete reconciliation rejected")
			}
		}
		if deleteErr == nil && discardRef != "" {
			ok, err = r.call(tx.Context(), tx, "mark_publication_discarded", discardRef, now)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("publication discard confirmation rejected")
			}
		}
		return nil
	})
}
