package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
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
func nextVersion(tags []string) string {
	latest := version{}
	for _, tag := range tags {
		v, ok := parse(tag)
		if ok && latest.less(v) {
			latest = v
		}
	}
	return fmt.Sprintf("v%d.%d.0", latest.major, latest.minor+1)
}
func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return strings.TrimSpace(string(out)), err
}

type plan struct {
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Date     string `json:"date"`
	Existing bool   `json:"existing"`
}

func releasePlan() (plan, error) {
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
	tags := strings.Fields(atHead)
	sort.Strings(tags)
	for _, tag := range tags {
		if _, ok := parse(tag); ok {
			return plan{tag, commit, date, true}, nil
		}
	}
	all, err := git("tag", "--list", "v*")
	if err != nil {
		return plan{}, err
	}
	return plan{nextVersion(strings.Fields(all)), commit, date, false}, nil
}
func main() {
	plan, err := releasePlan()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(plan); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
