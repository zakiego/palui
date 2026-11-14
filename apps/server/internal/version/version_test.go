package version_test

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/version"
)

// Ports packages/core/src/build-info.test.ts.

var (
	A = strings.Repeat("a", 40)
	B = strings.Repeat("b", 40)
	C = strings.Repeat("c", 40)
)

func TestLoadBuildInfoReadsTheDeployFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "build-info.json")
	os.WriteFile(path, []byte(`{"commit":"`+A+`","history":[{"sha":"`+A+`","subjects":["x"]}]}`), 0o644)
	got := version.LoadBuildInfo(path)
	want := &version.BuildInfo{Commit: A, History: []version.HistoryEntry{{Sha: A, Subjects: []string{"x"}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
}

func TestLoadBuildInfoNilForNoPathMissingFileOrBadContent(t *testing.T) {
	dir := t.TempDir()
	if version.LoadBuildInfo("") != nil || version.LoadBuildInfo(filepath.Join(dir, "nope.json")) != nil {
		t.Fatal("want nil")
	}
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{not json"), 0o644)
	if version.LoadBuildInfo(bad) != nil {
		t.Fatal("bad json")
	}
	if version.ParseBuildInfo([]byte(`{"commit":""}`)) != nil || version.ParseBuildInfo([]byte(`{"commit":"not-a-sha"}`)) != nil {
		t.Fatal("bad commit")
	}
	got := version.ParseBuildInfo([]byte(`{"commit":"` + A + `"}`))
	if !reflect.DeepEqual(got, &version.BuildInfo{Commit: A, History: []version.HistoryEntry{}}) {
		t.Fatalf("%+v", got)
	}
}

func TestSubjectsSince(t *testing.T) {
	info := version.BuildInfo{Commit: C, History: []version.HistoryEntry{{Sha: C, Subjects: []string{"c1", "c2"}}, {Sha: B, Subjects: []string{"b"}}, {Sha: A, Subjects: []string{"a"}}}}
	check := func(prev string, want ...string) {
		t.Helper()
		if got := version.SubjectsSince(info, prev); !reflect.DeepEqual(got, want) {
			t.Errorf("since %q: %v", prev, got)
		}
	}
	check(A, "c1", "c2", "b")
	check(B, "c1", "c2")
	check("", "c1", "c2")
	check(strings.Repeat("d", 40), "c1", "c2")
	check(B[:7], "c1", "c2")
}

func TestShortSha(t *testing.T) {
	if version.ShortSha(A) != "aaaaaaa" || version.ShortSha("abc") != "abc" {
		t.Fatal("ShortSha")
	}
}

func TestRootPackageJSONHasAVersion(t *testing.T) {
	v, err := version.FromPackageJSON(filepath.Join("..", "..", "..", "..", "package.json"))
	if err != nil || !regexp.MustCompile(`^\d+\.\d+\.\d+`).MatchString(v) {
		t.Fatalf("%q %v", v, err)
	}
}
