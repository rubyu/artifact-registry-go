package artifactregistry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"github.com/rubyu/artifact-registry-go/dbcore"
	"github.com/rubyu/artifact-registry-go/txscope"
)

type pythonFixture struct {
	UpstreamCommit string `json:"upstream_commit"`
	Digests        []struct {
		Payload string `json:"payload_base64"`
		Digest  string `json:"digest_value"`
	} `json:"digests"`
	Locks []struct {
		Key   string `json:"key"`
		Value int64  `json:"value"`
	} `json:"lock_ids"`
	Refs []struct {
		Input      string  `json:"input"`
		Normalized *string `json:"normalized"`
	} `json:"refs"`
	Flow    map[string]any     `json:"flow"`
	Catalog map[string][][]any `json:"catalog"`
}

func TestIntegrationPythonSchemaCompatibility(t *testing.T) {
	fixture := loadPythonFixture(t)
	h := integrationHarness(t)
	data, err := os.ReadFile("testdata/compatibility/catalog-queries.json")
	if err != nil {
		t.Fatal(err)
	}
	var queries []struct{ Name, SQL string }
	if err = json.Unmarshal(data, &queries); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Catalog) != len(queries) {
		t.Fatal("Python catalog fixture is incomplete")
	}
	for _, query := range queries {
		t.Run(query.Name, func(t *testing.T) {
			rows, err := h.admin.Query(query.SQL, h.config.Schema)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			result := [][]any{}
			for rows.Next() {
				values := make([]any, len(columns))
				dest := make([]any, len(columns))
				for i := range values {
					dest[i] = &values[i]
				}
				if err = rows.Scan(dest...); err != nil {
					t.Fatal(err)
				}
				for i, value := range values {
					switch typed := value.(type) {
					case []byte:
						values[i] = normalizeCatalog(string(typed), h.config.Schema)
					case string:
						values[i] = normalizeCatalog(typed, h.config.Schema)
					}
				}
				result = append(result, values)
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			sameJSON(t, result, fixture.Catalog[query.Name])
		})
	}
}
func normalizeCatalog(value, schema string) string {
	value = strings.ReplaceAll(value, schema, "__SCHEMA__")
	value = strings.ReplaceAll(value, `"`, "")
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
}

func loadPythonFixture(t *testing.T) pythonFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/compatibility/python-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture pythonFixture
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.UpstreamCommit != "46d82c12a9323a2c013c272fbf845891b5602322" {
		t.Fatal("unexpected Python reference commit")
	}
	return fixture
}
func TestPythonValueCompatibility(t *testing.T) {
	fixture := loadPythonFixture(t)
	for _, test := range fixture.Digests {
		payload, err := base64.StdEncoding.DecodeString(test.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if value := dbcore.SHA256DigestValue(payload); value != test.Digest {
			t.Fatal(value, test.Digest)
		}
	}
	for _, test := range fixture.Locks {
		if value := dbcore.LockID(test.Key); value != test.Value {
			t.Fatal(test.Key, value, test.Value)
		}
	}
	for _, test := range fixture.Refs {
		value, ok := normalizeRef(test.Input)
		if (test.Normalized != nil) != ok || (ok && value != *test.Normalized) {
			t.Fatal(test.Input, value, ok)
		}
	}
}
func sameJSON(t *testing.T, got, want any) {
	t.Helper()
	actual, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var normalized any
	if err = json.Unmarshal(actual, &normalized); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(normalized, want) {
		t.Fatalf("Python comparison mismatch\nGo: %s\nPython: %v", actual, want)
	}
}
func TestIntegrationPythonFlowCompatibility(t *testing.T) {
	fixture := loadPythonFixture(t)
	h := integrationHarness(t)
	ctx := context.Background()
	payload, err := base64.StdEncoding.DecodeString(fixture.Flow["payload_base64"].(string))
	if err != nil {
		t.Fatal(err)
	}
	status := func(ref string) map[string]any {
		d := h.diagnostic(t, ref)
		return map[string]any{"publication_status": d.PublicationStatus, "object_status": d.ObjectStatus}
	}
	p, err := h.r.PrepareBytes(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	prepared := status(p.Ref())
	prepared["digest_value"] = p.Info().DigestValue
	prepared["size_bytes"] = p.Info().SizeBytes
	flow := map[string]any{"payload_base64": base64.StdEncoding.EncodeToString(payload), "prepared": prepared}
	if err = h.r.RunTx(ctx, func(tx *txscope.Scope) error {
		before, err := h.r.LookupCommittedPublication(tx.Context(), tx, p.Ref())
		if err != nil {
			return err
		}
		flow["before_commit_lookup"] = before != nil
		_, err = h.r.CommitPrepared(tx.Context(), tx, p)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	flow["after_commit"] = status(p.Ref())
	var artifact *CommittedArtifact
	if err = h.r.RunTx(ctx, func(tx *txscope.Scope) error {
		var err error
		artifact, err = h.r.LookupCommittedPublication(tx.Context(), tx, p.Ref())
		if err != nil {
			return err
		}
		_, err = h.r.ReadBytes(tx.Context(), artifact)
		flow["during_tx_read_rejected"] = err != nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err := h.r.ReadBytes(ctx, artifact)
	if err != nil {
		t.Fatal(err)
	}
	flow["read_base64"] = base64.StdEncoding.EncodeToString(data)
	data, err = h.r.ReadRangeBytes(ctx, artifact, 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	flow["range_base64"] = base64.StdEncoding.EncodeToString(data)
	if err = h.r.RunTx(ctx, func(tx *txscope.Scope) error {
		results, err := h.r.RequestDiscardMany(tx.Context(), tx, []string{p.Ref(), p.Ref(), "00000000-0000-0000-0000-000000000000"})
		if err != nil {
			return err
		}
		statuses := []string{}
		for _, result := range results {
			statuses = append(statuses, result.Status)
		}
		flow["discard_statuses"] = statuses
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	summary, err := h.m.RunCleanupCycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flow["cleanup"] = map[string]any{"cleaned_publication_count": summary.CleanedPublicationCount, "deleted_object_count": summary.DeletedObjectCount, "delete_failed_count": summary.DeleteFailedCount, "status": summary.Status}
	flow["after_cleanup"] = status(p.Ref())
	p, err = h.r.PrepareBytes(ctx, []byte("abort"))
	if err != nil {
		t.Fatal(err)
	}
	if err = h.r.AbortPrepared(ctx, p); err != nil {
		t.Fatal(err)
	}
	flow["after_abort"] = status(p.Ref())
	p, err = h.r.PrepareBytes(ctx, []byte("rollback"))
	if err != nil {
		t.Fatal(err)
	}
	primary := errors.New("domain failure")
	err = h.r.RunTx(ctx, func(tx *txscope.Scope) error {
		if _, err := h.r.CommitPrepared(tx.Context(), tx, p); err != nil {
			return err
		}
		return primary
	})
	if !errors.Is(err, primary) {
		t.Fatal(err)
	}
	flow["after_domain_failure"] = status(p.Ref())
	p, err = h.r.PrepareBytes(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	flow["empty_payload"] = map[string]any{"digest_value": p.Info().DigestValue, "size_bytes": p.Info().SizeBytes}
	if err = h.r.AbortPrepared(ctx, p); err != nil {
		t.Fatal(err)
	}
	sameJSON(t, flow, fixture.Flow)
}
