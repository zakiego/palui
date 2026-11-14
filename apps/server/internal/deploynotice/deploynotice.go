// Package deploynotice ports deploy-notice.ts: it tells the owner when a new
// build is running (a new version, or a new commit on the same version). Off by
// default; the owner turns it on per chat, and each of those chats gets one
// message through the bot that answers there. Called once on start.
package deploynotice

import (
	"fmt"
	"slices"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/version"
)

const ChangelogURL = "https://palui.zakiego.com/docs/changelog"

const maxSubjects = 8

// Result is DeployNoticeResult: Sent with the chats, or not with a Reason
// (unchanged, first-run or off).
type Result struct {
	Sent    bool     `json:"sent"`
	Reason  string   `json:"reason,omitempty"`
	ChatIDs []string `json:"chatIds,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
}

// Change is what runs now and what ran before. Commit "" means a build
// without build-info.json; then only the version decides.
type Change struct {
	Version         string
	PreviousVersion string
	Commit          string
	PreviousCommit  string
	// Commit subjects since the previous commit, newest first.
	Subjects []string
}

// Text is deployNoticeText.
func Text(c Change) string {
	if c.Version != c.PreviousVersion {
		at := ""
		if c.Commit != "" {
			at = ", commit " + version.ShortSha(c.Commit)
		}
		return fmt.Sprintf("Palui sudah jalan di versi %s (sebelumnya %s)%s. Yang baru: %s", c.Version, c.PreviousVersion, at, ChangelogURL)
	}
	was := ""
	if c.PreviousCommit != "" {
		was = " (sebelumnya " + version.ShortSha(c.PreviousCommit) + ")"
	}
	text := fmt.Sprintf("Palui di-update ke commit %s%s, versi %s", version.ShortSha(c.Commit), was, c.Version)
	if len(c.Subjects) > 0 {
		var shown []string
		for i, s := range c.Subjects {
			if i >= maxSubjects {
				break
			}
			shown = append(shown, "- "+s)
		}
		if len(c.Subjects) > maxSubjects {
			shown = append(shown, fmt.Sprintf("- dan %d lainnya", len(c.Subjects)-maxSubjects))
		}
		text += "\n\n" + strings.Join(shown, "\n")
	}
	return text
}

func chatsWanted(q db.Q) ([]string, error) { return settings.Get[[]string](q, "deployNoticeChats") }

// On is deployNoticeOn.
func On(q db.Q, chatID string) (bool, error) {
	ids, err := chatsWanted(q)
	return slices.Contains(ids, chatID), err
}

// Set is setDeployNotice: turn one chat on or off.
func Set(q db.Q, chatID string, on bool, now int64) error {
	ids, err := chatsWanted(q)
	if err != nil {
		return err
	}
	current := []string{}
	for _, id := range ids {
		if id != chatID {
			current = append(current, id)
		}
	}
	if on {
		current = append(current, chatID)
	}
	return settings.Set(q, "deployNoticeChats", current, now)
}

// Announce is announceDeploy: records the running version and commit every
// time, and sends the notice to the chats that have it on when the build is
// new. One transaction: the version and the outbox rows land together.
func Announce(q db.Q, ver string, build *version.BuildInfo, now int64) (Result, error) {
	var r Result
	err := q.Tx(func(tx db.Q) error {
		var err error
		r, err = record(tx, ver, build, now)
		return err
	})
	if err == nil && r.Sent && len(r.ChatIDs) > 0 {
		outbox.Notify()
	}
	return r, err
}

func record(q db.Q, ver string, build *version.BuildInfo, now int64) (Result, error) {
	previousVersion, err := settings.Get[*string](q, "lastRunVersion")
	if err != nil {
		return Result{}, err
	}
	previousCommit, err := settings.Get[*string](q, "lastRunCommit")
	if err != nil {
		return Result{}, err
	}
	commit := ""
	if build != nil {
		commit = build.Commit
	}
	prevCommit := ""
	if previousCommit != nil {
		prevCommit = *previousCommit
	}
	newCommit := commit != "" && (previousCommit == nil || commit != prevCommit)
	if previousVersion != nil && *previousVersion == ver && !newCommit {
		return Result{Sent: false, Reason: "unchanged"}, nil
	}
	if err := settings.Set(q, "lastRunVersion", &ver, now); err != nil {
		return Result{}, err
	}
	// A build without a commit leaves the last known one.
	if commit != "" {
		if err := settings.Set(q, "lastRunCommit", &commit, now); err != nil {
			return Result{}, err
		}
	}
	if previousVersion == nil {
		return Result{Sent: false, Reason: "first-run"}, nil
	}
	wanted, err := chatsWanted(q)
	if err != nil {
		return Result{}, err
	}
	if len(wanted) == 0 {
		return Result{Sent: false, Reason: "off"}, nil
	}
	// A chat that was removed or switched off is skipped, not sent to.
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(wanted)), ", ")
	args := make([]any, len(wanted))
	for i, id := range wanted {
		args[i] = id
	}
	rows, err := q.Query("SELECT "+db.ChatCols+" FROM chats WHERE id IN ("+marks+")", args...)
	all, err := db.All(rows, err, db.ScanChat)
	if err != nil {
		return Result{}, err
	}
	var chats []db.Chat
	for _, c := range all {
		if c.Allowed {
			chats = append(chats, c)
		}
	}
	var subjects []string
	if build != nil && newCommit {
		subjects = version.SubjectsSince(*build, prevCommit)
	}
	shownPrev := ""
	if newCommit {
		shownPrev = prevCommit
	}
	text := Text(Change{Version: ver, PreviousVersion: *previousVersion, Commit: commit, PreviousCommit: shownPrev, Subjects: subjects})
	source := "deploy:" + ver
	if commit != "" {
		source += ":" + version.ShortSha(commit)
	}
	exp := now + 24*3600_000
	sent := []string{}
	for _, c := range chats {
		if _, err := outbox.Enqueue(q, outbox.EnqueueInput{ChatID: c.ID, Channel: c.Channel, ConnectorID: c.ConnectorID, Payload: db.OutboxPayload{Text: text}, Source: source, ExpiresAt: &exp}, now); err != nil {
			return Result{}, err
		}
		sent = append(sent, c.ID)
	}
	skipped := []string{}
	for _, id := range wanted {
		if !slices.Contains(sent, id) {
			skipped = append(skipped, id)
		}
	}
	return Result{Sent: true, ChatIDs: sent, Skipped: skipped}, nil
}
