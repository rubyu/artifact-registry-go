package dbcore

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestMigrationCLIPayload(t *testing.T) {
	for _, failure := range []bool{false, true} {
		var output bytes.Buffer
		code := RunMigrationCLI(&output, func() ([]MigrationStepResult, error) {
			if failure {
				return nil, errors.New("failure")
			}
			return []MigrationStepResult{{TargetName: "test", Status: "ok", SchemaName: "test"}}, nil
		})
		var body map[string]any
		if err := json.Unmarshal(output.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		steps, ok := body["steps"].([]any)
		if !ok {
			t.Fatal(body)
		}
		if failure {
			if code != 1 || body["status"] != "error" || body["message"] != "failure" || len(steps) != 0 {
				t.Fatal(code, body)
			}
		} else if code != 0 || body["status"] != "ok" || len(steps) != 1 {
			t.Fatal(code, body)
		}
	}
}
