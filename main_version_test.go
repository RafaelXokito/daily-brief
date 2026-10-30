package main

import "testing"

func TestIsRelease(t *testing.T) {
	cases := map[string]bool{
		"v0.1.0":                               true,
		"v1.2.3":                               true,
		"v10.20.30":                            true,
		"":                                     false,
		"(devel)":                              false,
		"0.1.0":                                false,
		"v1.2":                                 false,
		"v1.2.3-rc.1":                          false,
		"v1.2.3+dirty":                         false,
		"v0.1.1-0.20261030120000-abcdef123456": false,
		"v0.1.1-0.20261030120000-abcdef123456+dirty": false,
		"0.1.1-SNAPSHOT-abc1234":                     false,
		" v1.2.3":                                    false,
		"v1.2.3\n":                                   false,
	}
	for v, want := range cases {
		if got := isRelease(v); got != want {
			t.Errorf("isRelease(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestReleaseVersionUsesLdflag(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = "v0.3.1"
	if v, ok := releaseVersion(); v != "v0.3.1" || !ok {
		t.Errorf("releaseVersion() = %q, %v; want v0.3.1, true", v, ok)
	}
	if got := versionString(); got != "daily-brief v0.3.1" {
		t.Errorf("versionString() = %q", got)
	}

	version = "0.3.2-SNAPSHOT-abc1234"
	if v, ok := releaseVersion(); v != version || ok {
		t.Errorf("releaseVersion() = %q, %v; want snapshot, false", v, ok)
	}

	// Without the ldflag, test binaries report the build info's "(devel)"
	// or a pseudo-version, never a release.
	version = ""
	if v, ok := releaseVersion(); v == "" || ok {
		t.Errorf("releaseVersion() without ldflag = %q, %v; want a non-release", v, ok)
	}
}

func TestFormatVersion(t *testing.T) {
	if got := formatVersion("v0.1.0", true); got != "daily-brief v0.1.0" {
		t.Errorf("release: %q", got)
	}
	if got := formatVersion("v0.1.1-0.20261030-abc+dirty", false); got != "daily-brief dev (v0.1.1-0.20261030-abc+dirty)" {
		t.Errorf("dev: %q", got)
	}
	if got := formatVersion("(devel)", false); got != "daily-brief dev" {
		t.Errorf("devel: %q", got)
	}
}
