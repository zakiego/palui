// Package version ports packages/core/src/version.ts and build-info.ts: the
// Palui version and the commit a build was made from.
package version

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
)

// Version is the root package.json "version", stamped at build time with
// -ldflags "-X github.com/zakiego/palui/apps/server/internal/version.Version=X.Y.Z"
// (M10 wires the Docker build). A plain `go build` reports "0.0.0-dev".
var Version = "0.0.0-dev"

// FromPackageJSON reads "version" from a package.json file.
func FromPackageJSON(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		return "", err
	}
	return pkg.Version, nil
}

// HistoryEntry is one first-parent commit and the subjects it brought in.
type HistoryEntry struct {
	Sha      string   `json:"sha"`
	Subjects []string `json:"subjects"`
}

// BuildInfo is build-info.json, written by the deploy before `docker build`.
type BuildInfo struct {
	Commit string `json:"commit"`
	// Newest first, following first parents from Commit.
	History []HistoryEntry `json:"history"`
}

var commitRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// ParseBuildInfo validates raw JSON like the TS zod schema. Nil when invalid.
func ParseBuildInfo(raw []byte) *BuildInfo {
	var shape struct {
		Commit  *string            `json:"commit"`
		History *[]json.RawMessage `json:"history"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil || shape.Commit == nil || !commitRe.MatchString(*shape.Commit) {
		return nil
	}
	info := &BuildInfo{Commit: *shape.Commit, History: []HistoryEntry{}}
	if shape.History == nil {
		return info
	}
	for _, h := range *shape.History {
		var e struct {
			Sha      *string   `json:"sha"`
			Subjects *[]string `json:"subjects"`
		}
		if err := json.Unmarshal(h, &e); err != nil || e.Sha == nil || e.Subjects == nil {
			return nil
		}
		info.History = append(info.History, HistoryEntry{Sha: *e.Sha, Subjects: *e.Subjects})
	}
	return info
}

// LoadBuildInfo never fails: no path, or a missing, unreadable or malformed
// file, is nil.
func LoadBuildInfo(path string) *BuildInfo {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return ParseBuildInfo(b)
}

// LoadBuildInfoFromEnv reads the file named by PALUI_BUILD_INFO.
func LoadBuildInfoFromEnv() *BuildInfo { return LoadBuildInfo(os.Getenv("PALUI_BUILD_INFO")) }

// ShortSha is the first 7 characters.
func ShortSha(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

// SubjectsSince lists the subjects between previous (exclusive) and the build's
// commit, newest first. An unknown previous commit gives only the head's subjects.
func SubjectsSince(info BuildInfo, previous string) []string {
	idx := -1
	if previous != "" {
		for i, h := range info.History {
			if h.Sha == previous || strings.HasPrefix(h.Sha, previous) {
				idx = i
				break
			}
		}
	}
	var entries []HistoryEntry
	if idx >= 0 {
		entries = info.History[:idx]
	} else if len(info.History) > 0 {
		entries = info.History[:1]
	}
	out := []string{}
	for _, h := range entries {
		out = append(out, h.Subjects...)
	}
	return out
}
