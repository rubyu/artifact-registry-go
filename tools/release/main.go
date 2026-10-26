package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var stable = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type version struct{ major, minor, patch int }

func parse(raw string) (version, bool) {
	parts := stable.FindStringSubmatch(raw)
	if parts == nil {
		return version{}, false
	}
	values := [3]int{}
	for i := range values {
		n, err := strconv.Atoi(parts[i+1])
		if err != nil {
			return version{}, false
		}
		values[i] = n
	}
	return version{values[0], values[1], values[2]}, true
}
func (v version) less(other version) bool {
	if v.major != other.major {
		return v.major < other.major
	}
	if v.minor != other.minor {
		return v.minor < other.minor
	}
	return v.patch < other.patch
}

type increment int

const (
	patchRelease increment = iota
	minorRelease
	majorRelease
)

func nextVersion(tags []string, bump increment) string {
	latest := version{}
	found := false
	for _, tag := range tags {
		v, ok := parse(tag)
		if ok && (!found || latest.less(v)) {
			latest = v
			found = true
		}
	}
	if !found {
		return "v0.1.0"
	}
	switch bump {
	case majorRelease:
		return fmt.Sprintf("v%d.0.0", latest.major+1)
	case minorRelease:
		return fmt.Sprintf("v%d.%d.0", latest.major, latest.minor+1)
	default:
		return fmt.Sprintf("v%d.%d.%d", latest.major, latest.minor, latest.patch+1)
	}
}

type pullRequest struct {
	MergeCommitSHA string  `json:"merge_commit_sha"`
	MergedAt       *string `json:"merged_at"`
	Base           struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// Only the merged PR that produced this main commit controls its release.
// PRs merged into develop, and open PRs sharing a commit, must not affect it.
func incrementForCommit(pulls []pullRequest, commit string) increment {
	bump := patchRelease
	for _, pr := range pulls {
		if pr.Base.Ref != "main" || pr.MergedAt == nil || pr.MergeCommitSHA != commit {
			continue
		}
		for _, label := range pr.Labels {
			switch label.Name {
			case "release:major":
				return majorRelease
			case "release:minor":
				bump = minorRelease
			}
		}
	}
	return bump
}
func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return strings.TrimSpace(string(out)), err
}

type plan struct {
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Date     string `json:"date"`
	Existing bool   `json:"-"`
}

func existingVersion(tags []string) (string, bool) {
	var latest version
	found := ""
	for _, tag := range tags {
		value, ok := parse(tag)
		if ok && (found == "" || latest.less(value)) {
			latest = value
			found = tag
		}
	}
	return found, found != ""
}

func releasePlan(pulls []pullRequest) (plan, error) {
	commit, err := git("rev-parse", "HEAD")
	if err != nil {
		return plan{}, err
	}
	date, err := git("show", "-s", "--format=%cI", commit)
	if err != nil {
		return plan{}, err
	}
	atHead, err := git("tag", "--points-at", commit)
	if err != nil {
		return plan{}, err
	}
	if tag, ok := existingVersion(strings.Fields(atHead)); ok {
		return plan{tag, commit, date, true}, nil
	}
	all, err := git("tag", "--list", "v*")
	if err != nil {
		return plan{}, err
	}
	return plan{nextVersion(strings.Fields(all), incrementForCommit(pulls, commit)), commit, date, false}, nil
}
func main() {
	pullsPath := flag.String("pull-requests", "", "GitHub commit-associated pull requests JSON file")
	flag.Parse()
	var pulls []pullRequest
	if *pullsPath != "" {
		data, err := os.ReadFile(*pullsPath)
		if err == nil {
			err = json.Unmarshal(data, &pulls)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	plan, err := releasePlan(pulls)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(plan); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
