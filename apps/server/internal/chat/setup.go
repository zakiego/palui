package chat

// Setup changes between turns. The owner keeps changing the agent's setup
// (a deploy, a connector, a tool permission, the agent's notes), and the model
// only sees the setup of the turn it's in. Without a word about the change it
// reads its earlier replies as mistakes: "tadi aku yang salah, tool-nya ada kok"
// when the tool really wasn't there before. Each message turn stores a
// snapshot on its session and the next turn diffs against it; the difference
// goes into the [Palui context] block once.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/deploynotice"
	"github.com/zakiego/palui/apps/server/internal/tools"
	"github.com/zakiego/palui/apps/server/internal/version"
)

// SetupSnapshot is what shaped one message turn. Text that can be long or
// private (persona, notes, chat rules) is kept as a short hash.
type SetupSnapshot struct {
	Version  string `json:"version"`
	Commit   string `json:"commit,omitempty"`
	Audience string `json:"audience"`
	// Connector id to its label and the tools this turn could run from it
	// (active and loadable through search), sorted.
	Connectors map[string]SetupConnector `json:"connectors"`
	Persona    string                    `json:"persona"`
	Notes      string                    `json:"notes"`
	ChatRules  string                    `json:"chatRules"`
}

// SetupConnector is one connector in a SetupSnapshot.
type SetupConnector struct {
	Label string   `json:"label"`
	Tools []string `json:"tools"`
}

// setupHash: a short fingerprint, "" for empty text.
func setupHash(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

// SetupInput is what TakeSetupSnapshot reads.
type SetupInput struct {
	Version   string
	Commit    string
	Audience  string
	Tools     *tools.Collected
	Persona   string
	Notes     string
	ChatRules string
}

// TakeSetupSnapshot builds the snapshot of a turn.
func TakeSetupSnapshot(i SetupInput) SetupSnapshot {
	s := SetupSnapshot{
		Version: i.Version, Commit: i.Commit, Audience: i.Audience,
		Connectors: map[string]SetupConnector{},
		Persona:    setupHash(i.Persona), Notes: setupHash(i.Notes), ChatRules: setupHash(i.ChatRules),
	}
	if i.Tools == nil {
		return s
	}
	for _, n := range i.Tools.Names() {
		m := i.Tools.Meta[n]
		if m == nil {
			continue
		}
		id, label, name := m.ConnectorID, m.Label, m.Tool
		if id == "" {
			id = "palui"
		}
		if label == "" {
			label = id
		}
		if name == "" {
			name = n
		}
		c := s.Connectors[id]
		c.Label = label
		c.Tools = append(c.Tools, name)
		s.Connectors[id] = c
	}
	for id, c := range s.Connectors {
		slices.Sort(c.Tools)
		s.Connectors[id] = c
	}
	return s
}

var audienceNames = map[string]string{
	tools.AudienceOwner:   "the owner's own chat",
	tools.AudienceMembers: "a chat with members",
	tools.AudienceOutside: "a chat with people who aren't the owner",
}

func audienceName(a string) string {
	if n, ok := audienceNames[a]; ok {
		return n
	}
	return a
}

const (
	maxSetupTools    = 6
	maxSetupSubjects = 5
)

func toolList(names []string) string {
	shown := strings.Join(names[:min(maxSetupTools, len(names))], ", ")
	if len(names) > maxSetupTools {
		shown += fmt.Sprintf(" and %d more", len(names)-maxSetupTools)
	}
	return shown
}

func toolCount(n int) string {
	if n == 1 {
		return "1 tool"
	}
	return fmt.Sprintf("%d tools", n)
}

// SetupChanges lists what differs between the previous turn's setup and this
// one's, one short line each. Subjects are the commit subjects of a new
// build, newest first. Nothing changed is nil.
func SetupChanges(prev, cur SetupSnapshot, subjects []string) []string {
	var out []string
	switch {
	case prev.Version != cur.Version:
		out = append(out, fmt.Sprintf("Palui was updated to version %s (it was %s). Changelog: %s", cur.Version, prev.Version, deploynotice.ChangelogURL))
	case prev.Commit != cur.Commit && cur.Commit != "":
		out = append(out, fmt.Sprintf("Palui was updated (version %s, new build %s).", cur.Version, version.ShortSha(cur.Commit)))
	}
	if len(out) > 0 && len(subjects) > 0 {
		shown := subjects[:min(maxSetupSubjects, len(subjects))]
		line := "What the update changed: " + strings.Join(shown, "; ")
		if len(subjects) > maxSetupSubjects {
			line += fmt.Sprintf("; and %d more", len(subjects)-maxSetupSubjects)
		}
		out = append(out, line+".")
	}
	if prev.Audience != cur.Audience && prev.Audience != "" {
		out = append(out, fmt.Sprintf("This chat now counts as %s (before, it counted as %s), so the tools you get here changed.", audienceName(cur.Audience), audienceName(prev.Audience)))
	}
	ids := make([]string, 0, len(cur.Connectors)+len(prev.Connectors))
	for id := range cur.Connectors {
		ids = append(ids, id)
	}
	for id := range prev.Connectors {
		if _, ok := cur.Connectors[id]; !ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	var added, removed []string
	for _, id := range ids {
		was, hadIt := prev.Connectors[id]
		now, hasIt := cur.Connectors[id]
		switch {
		case !hadIt:
			added = append(added, fmt.Sprintf("%s (%s)", now.Label, toolCount(len(now.Tools))))
		case !hasIt:
			removed = append(removed, was.Label)
		default:
			var plus, minus []string
			for _, t := range now.Tools {
				if !slices.Contains(was.Tools, t) {
					plus = append(plus, t)
				}
			}
			for _, t := range was.Tools {
				if !slices.Contains(now.Tools, t) {
					minus = append(minus, t)
				}
			}
			if len(plus) > 0 {
				out = append(out, fmt.Sprintf("New %s tools: %s.", now.Label, toolList(plus)))
			}
			if len(minus) > 0 {
				out = append(out, fmt.Sprintf("%s tools no longer available: %s.", now.Label, toolList(minus)))
			}
		}
	}
	if len(added) > 0 {
		out = append(out, "Now available in this chat: "+strings.Join(added, ", ")+".")
	}
	if len(removed) > 0 {
		out = append(out, "No longer available in this chat: "+strings.Join(removed, ", ")+".")
	}
	if prev.Persona != cur.Persona {
		out = append(out, "Your persona was edited.")
	}
	if prev.Notes != cur.Notes {
		out = append(out, "Your standing instructions or preferences were edited.")
	}
	if prev.ChatRules != cur.ChatRules {
		out = append(out, "This chat's rules were edited.")
	}
	return out
}

// SetupNote is the block for the turn context, "" without changes.
func SetupNote(changes []string) string {
	if len(changes) == 0 {
		return ""
	}
	lines := []string{"Since your last reply in this conversation, your setup changed:"}
	for _, c := range changes {
		lines = append(lines, "- "+c)
	}
	lines = append(lines, "This is background for you, not something to report. Mention it only when the newest message asks about it or about something it changed, or to explain an earlier reply that doesn't match what you can do or see now: that reply was right for its time, so explain the difference as this change, not as your mistake. Otherwise answer the message and leave the update out.")
	return strings.Join(lines, "\n")
}

var buildInfo = sync.OnceValue(version.LoadBuildInfoFromEnv)

// currentCommit is the running build's commit, "" when unknown.
func currentCommit() string {
	if info := buildInfo(); info != nil {
		return info.Commit
	}
	return ""
}

// buildSubjects: the commit subjects between the previous build and this one.
func buildSubjects(previousCommit string) []string {
	info := buildInfo()
	if info == nil || previousCommit == "" || previousCommit == info.Commit {
		return nil
	}
	return version.SubjectsSince(*info, previousCommit)
}

// SessionSetupNote diffs this turn's setup against the session's last one,
// stores this one, and returns the note ("" on a session's first turn or
// when nothing changed). The note shows once: the next turn diffs against
// this snapshot.
func SessionSetupNote(q db.Q, session *db.Session, cur SetupSnapshot, subjects func(previousCommit string) []string) (string, error) {
	if session == nil {
		return "", nil
	}
	raw, err := json.Marshal(cur)
	if err != nil {
		return "", err
	}
	note := ""
	if session.SetupSnapshot != nil {
		var prev SetupSnapshot
		if json.Unmarshal([]byte(*session.SetupSnapshot), &prev) == nil {
			var subj []string
			if subjects != nil && prev.Commit != cur.Commit {
				subj = subjects(prev.Commit)
			}
			note = SetupNote(SetupChanges(prev, cur, subj))
		}
		if *session.SetupSnapshot == string(raw) {
			return note, nil
		}
	}
	text := string(raw)
	if _, err := q.Exec("UPDATE sessions SET setup_snapshot = ? WHERE id = ?", text, session.ID); err != nil {
		return "", err
	}
	session.SetupSnapshot = &text
	return note, nil
}
