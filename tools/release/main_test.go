package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNextVersion(t *testing.T) {
	for _, test := range []struct {
		name string
		tags []string
		bump increment
		want string
	}{
		{"initial", nil, patchRelease, "v0.1.0"},
		{"initial minor", nil, minorRelease, "v0.1.0"},
		{"initial major", nil, majorRelease, "v0.1.0"},
		{"patch numeric order", []string{"v0.1.0", "v0.9.8", "v0.10.2", "v0.99.0-rc.1", "garbage"}, patchRelease, "v0.10.3"},
		{"patch across majors", []string{"v1.2.9", "v0.99.0", "v1.3.1"}, patchRelease, "v1.3.2"},
		{"minor resets patch", []string{"v1.9.8"}, minorRelease, "v1.10.0"},
		{"major resets minor and patch", []string{"v0.9.8"}, majorRelease, "v1.0.0"},
		{"invalid tags ignored", []string{"v01.1.0", "v1.0.0+meta"}, patchRelease, "v0.1.0"},
		{"zero tag exists", []string{"v0.0.0"}, patchRelease, "v0.0.1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := nextVersion(test.tags, test.bump); got != test.want {
				t.Fatalf("got %s, want %s", got, test.want)
			}
		})
	}
}

func TestIncrementForCommit(t *testing.T) {
	for _, test := range []struct {
		name, json string
		want       increment
	}{
		{"direct push", `[]`, patchRelease},
		{"no label", `[{"base":{"ref":"main"},"merged_at":"now","merge_commit_sha":"target","labels":[]}]`, patchRelease},
		{"minor", `[{"base":{"ref":"main"},"merged_at":"now","merge_commit_sha":"target","labels":[{"name":"release:minor"}]}]`, minorRelease},
		{"major wins", `[{"base":{"ref":"main"},"merged_at":"now","merge_commit_sha":"target","labels":[{"name":"release:minor"},{"name":"release:major"}]}]`, majorRelease},
		{"develop ignored", `[{"base":{"ref":"develop"},"merged_at":"now","merge_commit_sha":"target","labels":[{"name":"release:major"}]}]`, patchRelease},
		{"open PR ignored", `[{"base":{"ref":"main"},"merged_at":null,"merge_commit_sha":"target","labels":[{"name":"release:major"}]}]`, patchRelease},
		{"other commit ignored", `[{"base":{"ref":"main"},"merged_at":"now","merge_commit_sha":"other","labels":[{"name":"release:major"}]}]`, patchRelease},
		{"unrelated label ignored", `[{"base":{"ref":"main"},"merged_at":"now","merge_commit_sha":"target","labels":[{"name":"major"}]}]`, patchRelease},
	} {
		t.Run(test.name, func(t *testing.T) {
			var pulls []pullRequest
			if err := json.Unmarshal([]byte(test.json), &pulls); err != nil {
				t.Fatal(err)
			}
			if got := incrementForCommit(pulls, "target"); got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestExistingReleaseVersion(t *testing.T) {
	if got, ok := existingVersion([]string{"v0.99.0", "v1.2.0", "v1.10.0", "v2.0.0-rc.1"}); !ok || got != "v1.10.0" {
		t.Fatal(got, ok)
	}
	if _, ok := existingVersion([]string{"invalid"}); ok {
		t.Fatal("invalid version accepted")
	}
}

func TestReleasePlanInGitRepository(t *testing.T) {
	t.Chdir(t.TempDir())
	runGit := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "--initial-branch=main")
	runGit("config", "user.name", "Release test")
	runGit("config", "user.email", "release-test@example.invalid")
	runGit("-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "initial")
	first, err := releasePlan(nil)
	if err != nil || first.Version != "v0.1.0" || first.Existing {
		t.Fatalf("initial: %+v, %v", first, err)
	}
	if first.Commit != runGit("rev-parse", "HEAD") || len(first.Commit) != 40 || first.Date != runGit("show", "-s", "--format=%cI", "HEAD") {
		t.Fatalf("incorrect commit metadata: %+v", first)
	}
	runGit("-c", "tag.gpgsign=false", "tag", "v0.1.0")
	runGit("-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fix")
	patch, err := releasePlan(nil)
	if err != nil || patch.Version != "v0.1.1" || patch.Existing {
		t.Fatalf("patch: %+v, %v", patch, err)
	}
	var pulls []pullRequest
	data := `[{"base":{"ref":"main"},"merged_at":"now","merge_commit_sha":"` + patch.Commit + `","labels":[{"name":"release:minor"}]}]`
	if err := json.Unmarshal([]byte(data), &pulls); err != nil {
		t.Fatal(err)
	}
	minor, err := releasePlan(pulls)
	if err != nil || minor.Version != "v0.2.0" || minor.Existing {
		t.Fatalf("minor: %+v, %v", minor, err)
	}
	runGit("-c", "tag.gpgsign=false", "tag", "-a", "v0.2.0", "-m", "release")
	// A retry must reuse the published tag even if the PR label has changed.
	pulls[0].Labels[0].Name = "release:major"
	retry, err := releasePlan(pulls)
	if err != nil || retry.Version != minor.Version || !retry.Existing || retry.Commit != minor.Commit || retry.Date != minor.Date {
		t.Fatalf("retry: %+v, %v", retry, err)
	}
}

func TestCLIRejectsInvalidPullRequests(t *testing.T) {
	// Exercise the actual CLI: API/input failures must not silently publish a patch.
	source, err := filepath.Abs("main.go")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	badJSON := filepath.Join(dir, "pulls.json")
	if err := os.WriteFile(badJSON, []byte(`{"message":"API error"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", source, "-pull-requests", badJSON)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "cannot unmarshal") {
		t.Fatalf("invalid response accepted: %s, %v", out, err)
	}
}
