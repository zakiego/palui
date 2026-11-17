package chat

// Custom commands (PLAN 8.3): the router matched one of the owner's saved
// commands. The row keeps what was typed; its content gets the expanded
// prompt, which is what the model sees for that message (ToModelMessages),
// now and in later turns.

import (
	"fmt"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/customcmd"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// StoredCommand marks a user row that ran a custom command.
type StoredCommand struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Prompt   string `json:"prompt"`
	ReportID string `json:"reportId,omitempty"`
}

// CustomReportShared is the reply when a command that attaches a debug report
// is typed in a shared chat: the report holds the owner's private details.
const CustomReportShared = "/%s pakai debug report, jadi cuma jalan di chat pribadi kamu. Di sini pakai /report aja."

// startCustom writes the expanded prompt into the row. ok false when the
// command was answered without a turn.
func startCustom(c *EngineCtx, chat db.Chat, row db.Message, cmd db.CustomCommand, args string) (bool, error) {
	now := c.Clock()
	report, reportID := "", ""
	if cmd.AttachReport {
		shared, err := agents.IsSharedChat(c.DB, chat)
		if err != nil {
			return false, err
		}
		if shared {
			if err := handled(c.DB, row.ID); err != nil {
				return false, err
			}
			c.send(chat, fmt.Sprintf(CustomReportShared, cmd.Name), "system", SendExtra{})
			return false, nil
		}
		r, err := createReport(c, chat, args, row.ID, now)
		if err != nil {
			return false, err
		}
		report, reportID = r.Text, r.ID
	}
	content, err := jsonx.ParseObject(row.Content)
	if err != nil || content == nil {
		content = jsonx.NewMap[any]()
		content.Set("text", row.Text)
	}
	content.Set("command", StoredCommand{ID: cmd.ID, Name: cmd.Name, Prompt: customcmd.Expand(cmd, args, report), ReportID: reportID})
	raw, err := db.Raw(content)
	if err != nil {
		return false, err
	}
	_, err = c.DB.Exec("UPDATE messages SET content = ? WHERE id = ?", raw, row.ID)
	return err == nil, err
}
