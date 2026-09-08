package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The release config is only exercised when a tag is pushed, so a broken
// one is discovered at the worst possible moment. These tests pin the two
// things that actually break: a version symbol that no longer exists, and
// a platform silently dropped from the matrix.

const repoRoot = "../.."

// wantPlatforms is the published matrix: every goos x goarch pair.
var wantPlatforms = []string{
	"darwin/amd64", "darwin/arm64",
	"linux/amd64", "linux/arm64",
	"windows/amd64", "windows/arm64",
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// inlineList returns the items of a YAML flow sequence written as
// `key: [a, b, c]`, or nil when the key is absent.
func inlineList(content, key string) []string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		open := key + ": ["
		if !strings.HasPrefix(trimmed, open) {
			continue
		}
		body := strings.TrimSuffix(strings.TrimPrefix(trimmed, open), "]")
		var items []string
		for _, item := range strings.Split(body, ",") {
			if item = strings.TrimSpace(item); item != "" {
				items = append(items, item)
			}
		}
		return items
	}
	return nil
}

func TestGoreleaserStampsAnExistingVersionSymbol(t *testing.T) {
	cfg := readRepoFile(t, ".goreleaser.yml")

	stamps := regexp.MustCompile(`-X ([\w./]+)=`).FindAllStringSubmatch(cfg, -1)
	if len(stamps) == 0 {
		t.Fatal(".goreleaser.yml stamps no ldflags symbol; releases would report the default version")
	}
	src := readRepoFile(t, "cmd/tokentrace/main.go")
	for _, stamp := range stamps {
		symbol := stamp[1]
		pkg, name, ok := strings.Cut(symbol, ".")
		if !ok || pkg != "main" {
			t.Errorf("-X %s= stamps outside package main; the linker silently ignores unknown symbols", symbol)
			continue
		}
		if !strings.Contains(src, "var "+name+" = ") {
			t.Errorf("-X %s= but cmd/tokentrace/main.go declares no `var %s`", symbol, name)
		}
	}
}

func TestGoreleaserBuildMatrixCoversEveryPlatform(t *testing.T) {
	cfg := readRepoFile(t, ".goreleaser.yml")

	goos, goarch := inlineList(cfg, "goos"), inlineList(cfg, "goarch")
	if len(goos) == 0 || len(goarch) == 0 {
		t.Fatalf(".goreleaser.yml build matrix is empty: goos=%v goarch=%v", goos, goarch)
	}
	var got []string
	for _, sys := range goos {
		for _, arch := range goarch {
			got = append(got, fmt.Sprintf("%s/%s", sys, arch))
		}
	}
	assertSameSet(t, ".goreleaser.yml", got, wantPlatforms)
}

func TestMakefileCrossMatrixMatchesGoreleaser(t *testing.T) {
	mk := readRepoFile(t, "Makefile")

	var got []string
	for _, line := range strings.Split(mk, "\n") {
		if platforms, ok := strings.CutPrefix(line, "PLATFORMS :="); ok {
			got = strings.Fields(platforms)
			break
		}
	}
	if got == nil {
		t.Fatal("Makefile declares no PLATFORMS; `make cross` cannot stand in for goreleaser")
	}
	assertSameSet(t, "Makefile PLATFORMS", got, wantPlatforms)
}

func TestReleaseWorkflowFiresOnVersionTagsOnly(t *testing.T) {
	wf := readRepoFile(t, ".github/workflows/release.yml")

	tests := []struct {
		name string
		want string
		why  string
	}{
		{"tag trigger", `tags: ["v*"]`, "the workflow must fire on v* tags"},
		{"release permission", "contents: write", "goreleaser needs write access to create the GitHub release"},
		{"full history", "fetch-depth: 0", "the changelog needs the full git history"},
		{"clean release", "release --clean", "the release must build from a clean dist/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(wf, tt.want) {
				t.Errorf("release.yml is missing %q: %s", tt.want, tt.why)
			}
		})
	}
	if strings.Contains(wf, "branches:") {
		t.Error("release.yml triggers on branch pushes; releases must come from tags only")
	}
}

func assertSameSet(t *testing.T, what string, got, want []string) {
	t.Helper()
	gotSorted, wantSorted := append([]string(nil), got...), append([]string(nil), want...)
	sort.Strings(gotSorted)
	sort.Strings(wantSorted)
	if strings.Join(gotSorted, " ") != strings.Join(wantSorted, " ") {
		t.Errorf("%s platforms = %v, want %v", what, gotSorted, wantSorted)
	}
}
