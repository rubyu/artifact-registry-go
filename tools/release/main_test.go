package main

import "testing"

func TestNextMinorVersion(t *testing.T) {
	for _, test := range []struct {
		tags []string
		want string
	}{{nil, "v0.1.0"}, {[]string{"v0.1.0", "v0.9.8", "v0.10.2", "v0.99.0-rc.1", "garbage"}, "v0.11.0"}, {[]string{"v1.2.9", "v0.99.0", "v1.3.1"}, "v1.4.0"}, {[]string{"v01.1.0", "v1.0.0+meta"}, "v0.1.0"}} {
		if got := nextVersion(test.tags); got != test.want {
			t.Fatalf("%v: %s, want %s", test.tags, got, test.want)
		}
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
