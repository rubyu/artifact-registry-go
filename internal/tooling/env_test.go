package tooling

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestCLIConfigurationIsolation(t *testing.T) {
	for _, key := range []string{"HOST", "PORT", "USER", "PASSWORD", "DATABASE"} {
		t.Setenv("ARTIFACT_REGISTRY_POSTGRES_"+key, "runtime-only")
		t.Setenv("ARTIFACT_REGISTRY_MIGRATION_POSTGRES_"+key, "")
		t.Setenv("ARTIFACT_REGISTRY_MAINTENANCE_POSTGRES_"+key, "")
	}
	for _, workload := range []string{"migrate", "maintenance"} {
		var output bytes.Buffer
		if exit := Run(context.Background(), workload, nil, &output); exit != 1 {
			t.Fatal(exit)
		}
		var body map[string]any
		if err := json.Unmarshal(output.Bytes(), &body); err != nil || body["status"] != "error" {
			t.Fatal(output.String(), err)
		}
	}
}
func TestCLIVersionAndInvalidArguments(t *testing.T) {
	for _, workload := range []string{"migrate", "maintenance"} {
		var output bytes.Buffer
		if exit := Run(context.Background(), workload, []string{"--version"}, &output); exit != 0 {
			t.Fatal(exit)
		}
		var body map[string]any
		if err := json.Unmarshal(output.Bytes(), &body); err != nil || body["commit"] == nil || body["version"] == nil {
			t.Fatal(output.String(), err)
		}
		output.Reset()
		if Run(context.Background(), workload, []string{"unexpected"}, &output) != 1 {
			t.Fatal("invalid arguments")
		}
	}
}
func TestDurationValidation(t *testing.T) {
	for _, value := range []string{"0", "-1", "NaN", "+Inf", "1e99", "garbage"} {
		t.Setenv("TEST_DURATION", value)
		if _, err := positiveDuration("TEST_DURATION", "1", 1); err == nil {
			t.Fatal(value)
		}
	}
}
func TestWorkloadRole(t *testing.T) {
	for key, value := range map[string]string{"HOST": "localhost", "PORT": "5432", "USER": "wrong", "PASSWORD": "test", "DATABASE": "test"} {
		t.Setenv("ARTIFACT_REGISTRY_MAINTENANCE_POSTGRES_"+key, value)
	}
	t.Setenv("ARTIFACT_REGISTRY_DATABASE_MAINTENANCE_ROLE", "maintenance")
	if _, err := LoadDB("maintenance"); err == nil {
		t.Fatal("role mismatch")
	}
	t.Setenv("ARTIFACT_REGISTRY_MAINTENANCE_POSTGRES_USER", "maintenance")
	if _, err := LoadDB("maintenance"); err != nil {
		t.Fatal(err)
	}
}
