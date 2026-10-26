package artifactregistry

import "time"

type ObjectStatus string

const (
	Uploading    ObjectStatus = "uploading"
	Available    ObjectStatus = "available"
	UploadFailed ObjectStatus = "upload_failed"
	Deleted      ObjectStatus = "deleted"
	DeleteFailed ObjectStatus = "delete_failed"
)

type PublicationStatus string

const (
	Staged     PublicationStatus = "staged"
	Committed  PublicationStatus = "committed"
	Discarding PublicationStatus = "discarding"
	Discarded  PublicationStatus = "discarded"
	Aborted    PublicationStatus = "aborted"
)

type Artifact struct {
	Ref         string    `json:"ref"`
	DigestValue string    `json:"digest_value"`
	SizeBytes   int64     `json:"size_bytes"`
	UploadedAt  time.Time `json:"uploaded_at"`
}
type DiscardResult struct {
	Ref                string     `json:"ref"`
	Status             string     `json:"status"`
	DiscardRequestedAt *time.Time `json:"discard_requested_at"`
}

func (r DiscardResult) DiscardRequested() bool { return r.Status == "discard_requested" }
func (r DiscardResult) Idempotent() bool {
	return r.Status == "already_discarding" || r.Status == "already_discarded" || r.Status == "already_aborted"
}

type DiagnosticInfo struct {
	Ref, ObjectRef, ObjectPath                                                     string
	PublicationStatus                                                              PublicationStatus
	ObjectStatus                                                                   ObjectStatus
	DigestValue                                                                    string
	SizeBytes                                                                      *int64
	CommittedAt, DiscardRequestedAt, DiscardedAt, AbortedAt, UploadedAt, DeletedAt *time.Time
}
type PreparationError struct {
	Phase string
	Err   error
}

func (e *PreparationError) Error() string {
	return "artifact preparation (" + e.Phase + "): " + e.Err.Error()
}
func (e *PreparationError) Unwrap() error { return e.Err }

type IntegrityError struct{ Reason string }

func (e *IntegrityError) Error() string { return "artifact integrity: " + e.Reason }

type CleanupResult struct {
	CleanedPublicationCount int    `json:"cleaned_publication_count"`
	DeletedObjectCount      int    `json:"deleted_object_count"`
	DeleteFailedCount       int    `json:"delete_failed_count"`
	Status                  string `json:"status"`
}

func (r CleanupResult) TransitionCount() int {
	return r.CleanedPublicationCount + r.DeletedObjectCount + r.DeleteFailedCount
}
func (r CleanupResult) RetryableFailureCount() int { return r.DeleteFailedCount }
func (r *CleanupResult) finalize() {
	r.Status = "completed"
	if r.DeleteFailedCount > 0 {
		r.Status = "retryable"
	}
}

type QueueMetrics struct {
	EligibleCount    int      `json:"eligible_count"`
	OldestAgeSeconds *float64 `json:"oldest_age_seconds"`
}
type CleanupBacklog struct {
	StaleStaged  QueueMetrics `json:"stale_staged"`
	Aborted      QueueMetrics `json:"aborted"`
	Discarding   QueueMetrics `json:"discarding"`
	DeleteFailed QueueMetrics `json:"delete_failed"`
}
type MaintenanceSummary struct {
	StaleStagedResult       CleanupResult  `json:"stale_staged_result"`
	AbortedResult           CleanupResult  `json:"aborted_result"`
	DiscardingResult        CleanupResult  `json:"discarding_result"`
	DeleteFailedResult      CleanupResult  `json:"delete_failed_result"`
	RemainingBacklog        CleanupBacklog `json:"remaining_backlog"`
	CycleDurationSeconds    float64        `json:"cycle_duration_seconds"`
	LockWaitSeconds         float64        `json:"lock_wait_seconds"`
	CleanedPublicationCount int            `json:"cleaned_publication_count"`
	DeletedObjectCount      int            `json:"deleted_object_count"`
	DeleteFailedCount       int            `json:"delete_failed_count"`
	Status                  string         `json:"status"`
}
