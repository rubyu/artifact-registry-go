package dbcore

import (
	"encoding/json"
	"errors"
	"io"
)

type MigrationStepResult struct {
	TargetName      string  `json:"target_name"`
	Status          string  `json:"status"`
	SchemaName      string  `json:"schema_name"`
	CurrentRevision *string `json:"current_revision"`
	TargetRevision  *string `json:"target_revision"`
	Action          *string `json:"action"`
	LegacyVersion   *int64  `json:"legacy_version"`
	StampedRevision *string `json:"stamped_revision"`
	Message         *string `json:"message"`
}
type MigrationError struct {
	Err    error
	Result MigrationStepResult
}

func (e *MigrationError) Error() string { return e.Err.Error() }
func (e *MigrationError) Unwrap() error { return e.Err }

type MigrationCLIError struct {
	Err     error
	Results []MigrationStepResult
}

func (e *MigrationCLIError) Error() string { return e.Err.Error() }
func (e *MigrationCLIError) Unwrap() error { return e.Err }

// RunMigrationCLI は project-owned runner の結果を stdout JSON と exit code に変換する。
func RunMigrationCLI(out io.Writer, run func() ([]MigrationStepResult, error)) int {
	results, err := run()
	if results == nil {
		results = []MigrationStepResult{}
	}
	if err == nil {
		if json.NewEncoder(out).Encode(struct {
			Status string                `json:"status"`
			Steps  []MigrationStepResult `json:"steps"`
		}{"ok", results}) != nil {
			return 1
		}
		return 0
	}
	var cliError *MigrationCLIError
	var migrationError *MigrationError
	if errors.As(err, &cliError) {
		results = cliError.Results
		if results == nil {
			results = []MigrationStepResult{}
		}
	} else if errors.As(err, &migrationError) && len(results) == 0 {
		results = append(results, migrationError.Result)
	}
	_ = json.NewEncoder(out).Encode(struct {
		Status  string                `json:"status"`
		Message string                `json:"message"`
		Steps   []MigrationStepResult `json:"steps"`
	}{"error", err.Error(), results})
	return 1
}
