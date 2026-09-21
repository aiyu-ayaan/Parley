// Command release works out Parley's next version from commit subjects and keeps
// CHANGELOG.md. It is run by .github/workflows/release.yml.
//
// A release is asked for by a marker at the START of a commit subject on master:
//
//	!major   1.4.2         -> 2.0.0          stable
//	!feat    1.4.2         -> 1.5.0          stable
//	!fix     1.4.2         -> 1.4.3          stable
//	!alpha   1.4.2         -> 1.4.3-alpha.1  pre-release
//	         1.4.3-alpha.1 -> 1.4.3-alpha.2
//	!beta    1.4.3-alpha.2 -> 1.4.3-beta.1   promote, same base version
//	!stable  1.4.3-beta.1  -> 1.4.3          promote to stable, no bump
//
// When several pushed commits carry markers the strongest wins, in that order.
//
// Usage:
//
//	go run ./scripts/release next --current 1.4.2 --subjects-file subjects.txt
//	go run ./scripts/release notes --version 1.5.0 --subjects-file subjects.txt [--repository o/r]
//	go run ./scripts/release section --version 1.5.0
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Strongest first.
var markers = []string{"major", "feat", "fix", "stable", "beta", "alpha"}

var (
	versionRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-(alpha|beta)\.(\d+))?$`)
	markerRe  = regexp.MustCompile(`(?i)^\s*!(major|feat|fix|stable|beta|alpha)(\([^)]*\))?(\s*:\s*|\s+|$)`)
	typeRe    = regexp.MustCompile(`(?i)^(feat|fix|chore|docs|refactor|perf|test|build|ci|style|revert)(\([^)]*\))?(!)?\s*:\s*`)
	// Commits the release flow writes itself never trigger a release or show in notes.
	noiseRe = regexp.MustCompile(`(?i)^\s*(chore\(release\)|Merge (pull request|branch|remote-tracking))`)
)

func detectMarker(subjects []string) string {
	found := map[string]bool{}
	for _, s := range subjects {
		if noiseRe.MatchString(s) {
			continue
		}
		if m := markerRe.FindStringSubmatch(s); m != nil {
			found[strings.ToLower(m[1])] = true
		}
	}
	for _, m := range markers {
		if found[m] {
			return m
		}
	}
	return ""
}

func nextVersion(current, marker string) (string, error) {
	m := versionRe.FindStringSubmatch(strings.TrimSpace(current))
	if m == nil {
		return "", fmt.Errorf("invalid version %q, want X.Y.Z or X.Y.Z-alpha.N", current)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	channel := m[4]
	pre, _ := strconv.Atoi(m[5])
	base := func(a, b, c int) string { return fmt.Sprintf("%d.%d.%d", a, b, c) }

	switch marker {
	case "major":
		return base(major+1, 0, 0), nil
	case "feat":
		return base(major, minor+1, 0), nil
	case "fix":
		return base(major, minor, patch+1), nil
	case "stable":
		if channel == "" {
			return "", fmt.Errorf("!stable needs a pre-release to promote; %s is already stable", current)
		}
		return base(major, minor, patch), nil
	case "alpha", "beta":
		// A channel can be promoted but not demoted in place: alpha on a beta starts
		// over on the next patch, because X-alpha.1 sorts below a shipped X-beta.1.
		if channel == marker {
			return fmt.Sprintf("%s-%s.%d", base(major, minor, patch), marker, pre+1), nil
		}
		if channel == "alpha" && marker == "beta" {
			return base(major, minor, patch) + "-beta.1", nil
		}
		return fmt.Sprintf("%s-%s.1", base(major, minor, patch+1), marker), nil
	}
	return "", fmt.Errorf("unknown marker %q", marker)
}

// classify picks the CHANGELOG section for a subject.
func classify(subject string) string {
	kind := ""
	if m := markerRe.FindStringSubmatch(subject); m != nil {
		kind = strings.ToLower(m[1])
		subject = subject[len(m[0]):]
	}
	t := typeRe.FindStringSubmatch(subject)
	switch {
	case kind == "major", t != nil && t[3] == "!", strings.Contains(subject, "BREAKING CHANGE"):
		return "breaking"
	case kind == "feat", t != nil && strings.EqualFold(t[1], "feat"):
		return "features"
	case kind == "fix", t != nil && strings.EqualFold(t[1], "fix"):
		return "fixes"
	}
	return "other"
}

// clean strips the marker and type prefix, leaving what a reader wants.
func clean(subject string) string {
	if m := markerRe.FindStringSubmatch(subject); m != nil {
		subject = subject[len(m[0]):]
	}
	subject = strings.TrimRight(strings.TrimSpace(typeRe.ReplaceAllString(subject, "")), ".")
	if subject == "" {
		return ""
	}
	return strings.ToUpper(subject[:1]) + subject[1:]
}

func entry(version, repository, date string, subjects []string) string {
	sections := map[string][]string{}
	for _, s := range subjects {
		if strings.TrimSpace(s) == "" || noiseRe.MatchString(s) {
			continue
		}
		if c := clean(s); c != "" {
			sections[classify(s)] = append(sections[classify(s)], "- "+c)
		}
	}
	title := version
	if repository != "" {
		title = fmt.Sprintf("[%s](https://github.com/%s/releases/tag/v%s)", version, repository, version)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## %s (%s)\n", title, date)
	for _, s := range [][2]string{{"breaking", "### ⚠ Breaking changes"}, {"features", "### Features"}, {"fixes", "### Bug fixes"}, {"other", "### Other changes"}} {
		if lines := sections[s[0]]; len(lines) > 0 {
			fmt.Fprintf(&b, "\n%s\n\n%s\n", s[1], strings.Join(lines, "\n"))
		}
	}
	return b.String()
}

const changelogHead = "# Changelog\n"

// section returns version's entry from a CHANGELOG, without its heading.
func section(changelog, version string) string {
	var out []string
	in := false
	for _, line := range strings.Split(changelog, "\n") {
		if strings.HasPrefix(line, "## ") {
			if in {
				break
			}
			in = strings.HasPrefix(line, "## "+version+" ") || strings.HasPrefix(line, "## ["+version+"]")
			continue
		}
		if in {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func readLines(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	return strings.Split(string(b), "\n")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		fail(fmt.Errorf("usage: release next|notes|section [flags]"))
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	current := fs.String("current", "", "current version")
	version := fs.String("version", "", "version being released")
	subjects := fs.String("subjects-file", "", "file with one commit subject per line")
	repository := fs.String("repository", "", "owner/name, for release links")
	changelog := fs.String("changelog", "CHANGELOG.md", "changelog path")
	_ = fs.Parse(os.Args[2:])

	switch os.Args[1] {
	case "next":
		marker := detectMarker(readLines(*subjects))
		if marker == "" {
			fmt.Println("release=false")
			return
		}
		v, err := nextVersion(*current, marker)
		if err != nil {
			fail(err)
		}
		channel, pre := "stable", "false"
		if m := versionRe.FindStringSubmatch(v); m[4] != "" {
			channel, pre = m[4], "true"
		}
		fmt.Printf("release=true\nmarker=%s\nversion=%s\nchannel=%s\nprerelease=%s\n", marker, v, channel, pre)
	case "notes":
		old, _ := os.ReadFile(*changelog)
		rest := strings.TrimPrefix(string(old), changelogHead)
		e := entry(*version, *repository, time.Now().UTC().Format("2006-01-02"), readLines(*subjects))
		if err := os.WriteFile(*changelog, []byte(changelogHead+"\n"+e+"\n"+strings.TrimLeft(rest, "\n")), 0o644); err != nil {
			fail(err)
		}
	case "section":
		b, err := os.ReadFile(*changelog)
		if err != nil {
			fail(err)
		}
		fmt.Println(section(string(b), *version))
	default:
		fail(fmt.Errorf("unknown command %q", os.Args[1]))
	}
}
