package artifactregistry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/rubyu/artifact-registry-go/dbcore"
	"github.com/rubyu/artifact-registry-go/txscope"
)

type CommittedArtifact struct {
	info  Artifact
	path  string
	scope *txscope.Scope
	self  *CommittedArtifact
}

func (a *CommittedArtifact) Info() Artifact {
	if a == nil {
		return Artifact{}
	}
	return a.info
}
func (a *CommittedArtifact) Ref() string { return a.Info().Ref }
func normalizeRef(value string) (string, bool) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return "", false
	}
	return parsed.String(), true
}

func (r *Registry) LookupCommittedPublication(ctx context.Context, tx *txscope.Scope, ref string) (*CommittedArtifact, error) {
	if tx == nil {
		return nil, errors.New("transaction scope is required")
	}
	if err := tx.RequireOpen(); err != nil {
		return nil, err
	}
	ref, ok := normalizeRef(ref)
	if !ok {
		return nil, nil
	}
	if err := r.guardTx(ctx, tx); err != nil {
		return nil, err
	}
	a := &CommittedArtifact{scope: tx}
	err := tx.QueryRowContext(ctx, "SELECT p.artifact_ref::text,o.digest_value,o.size_bytes,o.uploaded_at,o.object_path FROM "+r.table("artifact_registry_publications")+" p JOIN "+r.table("artifact_registry_objects")+" o USING (artifact_object_ref) WHERE p.artifact_ref=$1 AND p.status='committed' AND o.status='available'", ref).Scan(&a.info.Ref, &a.info.DigestValue, &a.info.SizeBytes, &a.info.UploadedAt, &a.path)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.self = a
	return a, nil
}
func (r *Registry) LookupPublicationStatuses(ctx context.Context, tx *txscope.Scope, refs []string) (map[string]PublicationStatus, error) {
	if tx == nil {
		return nil, errors.New("transaction scope is required")
	}
	if err := tx.RequireOpen(); err != nil {
		return nil, err
	}
	result := map[string]PublicationStatus{}
	normalized := []string{}
	seen := map[string]bool{}
	for _, ref := range refs {
		if value, ok := normalizeRef(ref); ok && !seen[value] {
			normalized = append(normalized, value)
			seen[value] = true
		}
	}
	if len(normalized) == 0 {
		return result, nil
	}
	if err := r.guardTx(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT artifact_ref::text,status::text FROM "+r.table("artifact_registry_publications")+" WHERE artifact_ref=ANY($1::uuid[])", normalized)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ref string
		var status PublicationStatus
		if err = rows.Scan(&ref, &status); err != nil {
			return nil, err
		}
		result[ref] = status
	}
	return result, rows.Err()
}
func (r *Registry) readable(ctx context.Context, a *CommittedArtifact) error {
	if a == nil || a.self != a || a.scope == nil {
		return errors.New("committed artifact was not returned by the registry reader")
	}
	if err := a.scope.RequireSucceeded(); err != nil {
		return err
	}
	return txscope.AssertExternalIOAllowed(ctx)
}
func (r *Registry) ReadBytes(ctx context.Context, a *CommittedArtifact) ([]byte, error) {
	if err := r.readable(ctx, a); err != nil {
		return nil, err
	}
	data, err := r.store.Get(ctx, a.path, 0, -1)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != a.info.SizeBytes {
		return nil, &IntegrityError{"size_mismatch"}
	}
	if !dbcore.VerifyDigestValue(data, a.info.DigestValue) {
		return nil, &IntegrityError{"digest_mismatch"}
	}
	return data, nil
}
func (r *Registry) ReadRangeBytes(ctx context.Context, a *CommittedArtifact, start, length int64) ([]byte, error) {
	if err := r.readable(ctx, a); err != nil {
		return nil, err
	}
	if start < 0 || length <= 0 || start >= a.info.SizeBytes || length > a.info.SizeBytes-start {
		return nil, errors.New("invalid artifact byte range")
	}
	data, err := r.store.Get(ctx, a.path, start, length)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != length {
		return nil, errors.New("short object storage range response")
	}
	return data, nil
}
func (r *Registry) RequestDiscardMany(ctx context.Context, tx *txscope.Scope, refs []string) ([]DiscardResult, error) {
	if tx == nil {
		return nil, errors.New("transaction scope is required")
	}
	if err := tx.RequireOpen(); err != nil {
		return nil, err
	}
	normalized := make([]string, len(refs))
	for i, ref := range refs {
		value, ok := normalizeRef(ref)
		if !ok {
			return nil, fmt.Errorf("artifact ref must be a UUID string: %q", ref)
		}
		normalized[i] = value
	}
	if len(refs) == 0 {
		return []DiscardResult{}, nil
	}
	if err := r.guardTx(ctx, tx); err != nil {
		return nil, err
	}
	results := make([]DiscardResult, 0, len(refs))
	unique := make([]string, 0, len(normalized))
	seen := map[string]bool{}
	for _, ref := range normalized {
		if !seen[ref] {
			unique = append(unique, ref)
			seen[ref] = true
		}
	}
	sort.Strings(unique)
	byRef := map[string]DiscardResult{}
	now := r.now()
	for _, ref := range unique {
		changed, err := r.call(ctx, tx, "request_discard", ref, now)
		if err != nil {
			return nil, err
		}
		result := DiscardResult{Ref: ref}
		if changed {
			result.Status = "discard_requested"
			result.DiscardRequestedAt = &now
		} else {
			var status string
			err = tx.QueryRowContext(ctx, "SELECT status::text,discard_requested_at FROM "+r.table("artifact_registry_publications")+" WHERE artifact_ref=$1", ref).Scan(&status, &result.DiscardRequestedAt)
			if errors.Is(err, sql.ErrNoRows) {
				result.Status = "not_found"
			} else if err != nil {
				return nil, err
			} else {
				switch status {
				case "discarding", "discarded", "aborted":
					result.Status = "already_" + status
				default:
					result.Status = "unsupported_state"
				}
			}
		}
		byRef[ref] = result
	}
	for _, ref := range normalized {
		results = append(results, byRef[ref])
	}
	return results, nil
}

type Advanced struct{ Public *Registry }

func NewAdvanced(ctx context.Context, config Config, opts ...Option) (*Advanced, error) {
	r, err := New(ctx, config, opts...)
	if err != nil {
		return nil, err
	}
	return &Advanced{r}, nil
}
func AttachAdvanced(db *sql.DB, config Config, opts ...Option) (*Advanced, error) {
	r, err := Attach(db, config, opts...)
	if err != nil {
		return nil, err
	}
	return &Advanced{r}, nil
}
func (a *Advanced) Close() error { return a.Public.Close() }
func (a *Advanced) LookupPublicationDiagnostic(ctx context.Context, tx *txscope.Scope, ref string) (*DiagnosticInfo, error) {
	if tx == nil {
		return nil, errors.New("transaction scope is required")
	}
	if err := tx.RequireOpen(); err != nil {
		return nil, err
	}
	ref, ok := normalizeRef(ref)
	if !ok {
		return nil, nil
	}
	r := a.Public
	if err := r.guardTx(ctx, tx); err != nil {
		return nil, err
	}
	d := &DiagnosticInfo{}
	err := tx.QueryRowContext(ctx, "SELECT p.artifact_ref::text,o.artifact_object_ref::text,o.object_path,p.status::text,o.status::text,o.digest_value,o.size_bytes,p.committed_at,p.discard_requested_at,p.discarded_at,p.aborted_at,o.uploaded_at,o.deleted_at FROM "+r.table("artifact_registry_publications")+" p JOIN "+r.table("artifact_registry_objects")+" o USING (artifact_object_ref) WHERE p.artifact_ref=$1", ref).Scan(&d.Ref, &d.ObjectRef, &d.ObjectPath, &d.PublicationStatus, &d.ObjectStatus, &d.DigestValue, &d.SizeBytes, &d.CommittedAt, &d.DiscardRequestedAt, &d.DiscardedAt, &d.AbortedAt, &d.UploadedAt, &d.DeletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return d, err
}
