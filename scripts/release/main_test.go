package main

import (
	"strings"
	"testing"
)

func TestNextVersion(t *testing.T) {
	for _, c := range [][3]string{
		{"0.0.0", "fix", "0.0.1"},
		{"0.0.0", "feat", "0.1.0"},
		{"0.4.2", "major", "1.0.0"},
		{"1.4.2", "alpha", "1.4.3-alpha.1"},
		{"1.4.3-alpha.1", "alpha", "1.4.3-alpha.2"},
		{"1.4.3-alpha.2", "beta", "1.4.3-beta.1"},
		{"1.4.3-beta.1", "beta", "1.4.3-beta.2"},
		{"1.4.3-beta.2", "stable", "1.4.3"},
		{"1.4.3-beta.2", "alpha", "1.4.4-alpha.1"},
		{"1.4.3-beta.1", "fix", "1.4.4"},
	} {
		if got, err := nextVersion(c[0], c[1]); err != nil || got != c[2] {
			t.Errorf("%s + !%s = %q, %v; want %s", c[0], c[1], got, err, c[2])
		}
	}
	if _, err := nextVersion("1.0.0", "stable"); err == nil {
		t.Error("!stable on a stable version should fail")
	}
	if _, err := nextVersion("1.2", "fix"); err == nil {
		t.Error("bad version should fail")
	}
}

func TestDetectMarker(t *testing.T) {
	for _, c := range []struct {
		subjects []string
		want     string
	}{
		{[]string{"!feat(ui) : account rail"}, "feat"},
		{[]string{"!fix: crash"}, "fix"},
		{[]string{"feat(ui) : no marker"}, ""},
		{[]string{"fix: mentions !feat in passing"}, ""},
		{[]string{"!feature: not a marker"}, ""},
		{[]string{"!alpha: x", "!fix: y"}, "fix"},
		{[]string{"!feat: x", "!major: y"}, "major"},
		{[]string{"chore(release) : v1.0.0"}, ""},
	} {
		if got := detectMarker(c.subjects); got != c.want {
			t.Errorf("detectMarker(%q) = %q, want %q", c.subjects, got, c.want)
		}
	}
}

func TestEntryAndSection(t *testing.T) {
	e := entry("1.1.0", "o/r", "2026-09-21", []string{
		"!feat(ui) : add dark mode",
		"fix(backend) : stop the crash.",
		"docs : readme",
		"refactor(backend)! : drop old config",
		"chore(release) : v1.0.0",
		"Merge pull request #3 from x",
	})
	for _, want := range []string{
		"## [1.1.0](https://github.com/o/r/releases/tag/v1.1.0) (2026-09-21)",
		"### ⚠ Breaking changes\n\n- Drop old config",
		"### Features\n\n- Add dark mode",
		"### Bug fixes\n\n- Stop the crash\n",
		"### Other changes\n\n- Readme",
	} {
		if !strings.Contains(e, want) {
			t.Errorf("entry missing %q:\n%s", want, e)
		}
	}
	if strings.Contains(e, "v1.0.0") || strings.Contains(e, "Merge") {
		t.Errorf("entry kept release noise:\n%s", e)
	}

	log := changelogHead + "\n" + e + "\n## [1.0.0](x) (2026-09-01)\n\n### Features\n\n- Old\n"
	got := section(log, "1.1.0")
	if !strings.HasPrefix(got, "### ⚠ Breaking changes") || strings.Contains(got, "Old") {
		t.Errorf("section(1.1.0) = %q", got)
	}
	if got := section(log, "1.0.0"); got != "### Features\n\n- Old" {
		t.Errorf("section(1.0.0) = %q", got)
	}
}
