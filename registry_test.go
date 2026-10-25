package artifactregistry

import (
	"testing"
	"time"
)

func TestHandleAndCleanupValidation(t *testing.T) {
	r := &Registry{}
	if r.prepared(&PreparedArtifact{}) == nil || r.prepared(nil) == nil {
		t.Fatal("forged prepared handle")
	}
	if validateCleanup(CleanupSettings{}) == nil {
		t.Fatal("zero cleanup")
	}
	if validateCleanup(CleanupSettings{time.Hour, time.Hour, 1, time.Second, time.Minute}) != nil {
		t.Fatal("valid cleanup")
	}
}
