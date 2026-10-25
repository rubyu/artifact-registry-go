package artifactregistry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/rubyu/artifact-registry-go/dbcore"
	"github.com/rubyu/artifact-registry-go/txscope"
)

type Maintenance struct {
	registry *Registry
	settings CleanupSettings
}

func validateCleanup(s CleanupSettings) error {
	if s.StagedRetention <= 0 || s.AbortedRetention <= 0 || s.MaxBatchSize <= 0 || s.LockTimeout <= 0 || s.CycleTimeout <= 0 {
		return errors.New("cleanup retention, batch size and timeouts must be positive")
	}
	return nil
}
func NewMaintenance(ctx context.Context, config MaintenanceConfig, opts ...Option) (*Maintenance, error) {
	if err := validateCleanup(config.Cleanup); err != nil {
		return nil, err
	}
	db, err := config.Runtime.DB.Open(ctx, "")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	m, err := AttachMaintenance(db, config, opts...)
	if err != nil {
		db.Close()
		return nil, err
	}
	m.registry.ownsDB = true
	return m, nil
}
func AttachMaintenance(db *sql.DB, config MaintenanceConfig, opts ...Option) (*Maintenance, error) {
	if err := validateCleanup(config.Cleanup); err != nil {
		return nil, err
	}
	r, err := Attach(db, config.Runtime, opts...)
	if err != nil {
		return nil, err
	}
	r.maintenance = true
	return &Maintenance{r, config.Cleanup}, nil
}
func (m *Maintenance) Close() error { return m.registry.Close() }

type cleanupCandidate struct {
	ref, objectRef, path string
	publicationStatus    PublicationStatus
	objectStatus         ObjectStatus
}
type cleanupPhase struct {
	name, filter, order string
	args                []any
	delay               time.Duration
}

func (m *Maintenance) phases(now time.Time) []cleanupPhase {
	return []cleanupPhase{
		{"stale_staged", "p.status='staged' AND p.created_at <= $1", "p.created_at,p.artifact_publication_id", []any{now.Add(-m.settings.StagedRetention)}, m.settings.StagedRetention},
		{"aborted", "p.status='aborted' AND p.aborted_at <= $1 AND o.status NOT IN ('deleted','delete_failed')", "p.aborted_at,p.artifact_publication_id", []any{now.Add(-m.settings.AbortedRetention)}, m.settings.AbortedRetention},
		{"discarding", "p.status='discarding' AND p.discard_requested_at <= $1 AND o.status <> 'delete_failed'", "p.discard_requested_at,p.artifact_publication_id", []any{now}, 0},
		{"delete_failed", "o.status='delete_failed' AND p.status IN ('aborted','discarding')", "o.updated_at,o.artifact_object_id", nil, 0},
	}
}
func (m *Maintenance) RunCleanupCycle(ctx context.Context) (summary MaintenanceSummary, err error) {
	started := time.Now()
	cycle, cancel := context.WithTimeout(ctx, m.settings.CycleTimeout)
	defer cancel()
	r := m.registry
	lock, err := dbcore.AcquireLock(cycle, r.db, "artifact-registry/cleanup/"+r.schema, false, m.settings.LockTimeout)
	if err != nil {
		return summary, err
	}
	defer func() {
		err = errors.Join(err, lock.Release())
		summary.CycleDurationSeconds = time.Since(started).Seconds()
	}()
	if err = lock.AddShared(cycle, "artifact-registry/"+r.schema, m.settings.LockTimeout); err != nil {
		return summary, err
	}
	summary.LockWaitSeconds = time.Since(started).Seconds()
	results := []*CleanupResult{&summary.StaleStagedResult, &summary.AbortedResult, &summary.DiscardingResult, &summary.DeleteFailedResult}
	for i, phase := range m.phases(r.clock.Now().UTC()) {
		if err = cycle.Err(); err != nil {
			return summary, err
		}
		*results[i], err = m.cleanup(cycle, phase)
		if err != nil {
			return summary, err
		}
	}
	summary.RemainingBacklog, err = m.backlog(cycle)
	if err != nil {
		return summary, err
	}
	for _, result := range results {
		summary.CleanedPublicationCount += result.CleanedPublicationCount
		summary.DeletedObjectCount += result.DeletedObjectCount
		summary.DeleteFailedCount += result.DeleteFailedCount
	}
	summary.Status = "completed"
	if summary.DeleteFailedCount > 0 {
		summary.Status = "retryable"
	}
	return summary, nil
}
func (m *Maintenance) join() string {
	r := m.registry
	return r.table("artifact_registry_publications") + " p JOIN " + r.table("artifact_registry_objects") + " o USING (artifact_object_ref)"
}
func (m *Maintenance) cleanup(ctx context.Context, phase cleanupPhase) (result CleanupResult, err error) {
	r := m.registry
	args := append([]any{}, phase.args...)
	args = append(args, m.settings.MaxBatchSize)
	candidates := []cleanupCandidate{}
	err = r.mutation(ctx, func(tx *txscope.Scope) error {
		rows, err := tx.QueryContext(tx.Context(), "SELECT p.artifact_ref::text,p.artifact_object_ref::text,o.object_path,p.status::text,o.status::text FROM "+m.join()+" WHERE "+phase.filter+" ORDER BY "+phase.order+fmt.Sprintf(" LIMIT $%d", len(args)), args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c cleanupCandidate
			if err = rows.Scan(&c.ref, &c.objectRef, &c.path, &c.publicationStatus, &c.objectStatus); err != nil {
				return err
			}
			candidates = append(candidates, c)
		}
		return rows.Err()
	})
	if err != nil {
		return result, err
	}
	for _, candidate := range candidates {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		now := r.clock.Now().UTC()
		if candidate.publicationStatus == Staged {
			var changed bool
			err = r.mutation(ctx, func(tx *txscope.Scope) error {
				var e error
				changed, e = r.call(tx.Context(), tx, "abort_publication", candidate.ref, candidate.objectRef, now)
				return e
			})
			if err != nil {
				return result, err
			}
			if !changed {
				continue
			}
		}
		if candidate.objectStatus == Deleted {
			if candidate.publicationStatus == Discarding {
				err = r.mutation(ctx, func(tx *txscope.Scope) error {
					ok, e := r.call(tx.Context(), tx, "mark_publication_discarded", candidate.ref, now)
					if e != nil {
						return e
					}
					if !ok {
						return errors.New("discard confirmation rejected")
					}
					return nil
				})
				if err != nil {
					return result, err
				}
				result.CleanedPublicationCount++
			} else if candidate.publicationStatus == Staged {
				result.CleanedPublicationCount++
			}
			continue
		}
		// 開始した DELETE と状態更新は、親の cancellation 後も期限付きで完了させる。
		operation, cancel := r.ioContext(ctx)
		deleteErr := r.store.Delete(operation, candidate.path)
		var changed bool
		err = r.mutation(operation, func(tx *txscope.Scope) error {
			var e error
			if deleteErr != nil {
				changed, e = r.call(tx.Context(), tx, "mark_object_delete_failed", candidate.objectRef)
				return e
			}
			changed, e = r.call(tx.Context(), tx, "confirm_object_deleted", candidate.objectRef, now)
			if e != nil {
				return e
			}
			if !changed {
				return errors.New("object delete confirmation rejected")
			}
			if candidate.publicationStatus == Discarding {
				ok, e := r.call(tx.Context(), tx, "mark_publication_discarded", candidate.ref, now)
				if e != nil {
					return e
				}
				if !ok {
					return errors.New("publication discard confirmation rejected")
				}
			}
			return nil
		})
		cancel()
		if err != nil {
			return result, err
		}
		if deleteErr == nil {
			result.CleanedPublicationCount++
			result.DeletedObjectCount++
		} else if changed {
			result.DeleteFailedCount++
		}
		if err = ctx.Err(); err != nil {
			return result, err
		}
	}
	result.finalize()
	return result, nil
}
func (m *Maintenance) backlog(ctx context.Context) (CleanupBacklog, error) {
	backlog := CleanupBacklog{}
	metrics := []*QueueMetrics{&backlog.StaleStaged, &backlog.Aborted, &backlog.Discarding, &backlog.DeleteFailed}
	columns := []string{"p.created_at", "p.aborted_at", "p.discard_requested_at", "o.updated_at"}
	now := m.registry.clock.Now().UTC()
	err := m.registry.mutation(ctx, func(tx *txscope.Scope) error {
		for i, phase := range m.phases(now) {
			var oldest sql.NullTime
			if err := tx.QueryRowContext(tx.Context(), "SELECT COUNT(*),MIN("+columns[i]+") FROM "+m.join()+" WHERE "+phase.filter, phase.args...).Scan(&metrics[i].EligibleCount, &oldest); err != nil {
				return err
			}
			if oldest.Valid {
				age := now.Sub(oldest.Time).Seconds() - phase.delay.Seconds()
				if age < 0 {
					age = 0
				}
				metrics[i].OldestAgeSeconds = &age
			}
		}
		return nil
	})
	return backlog, err
}
