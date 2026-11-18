package rpcpeople

import (
	"net/http"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// apps/app/src/lib/fns/attachments.ts: files the agent saved from chats.
// Metadata only; the bytes go through GET /api/attachments/:id or a share link.

const fileGone = "That file doesn't exist anymore."

var attachmentID = zod.String().Min(1).Max(100)

type attachmentItem struct {
	ID          string  `json:"id"`
	ChatID      string  `json:"chatId"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Kind        string  `json:"kind"`
	MimeType    string  `json:"mimeType"`
	Filename    *string `json:"filename"`
	Size        int64   `json:"size"`
	CreatedAt   int64   `json:"createdAt"`
	Chat        string  `json:"chat"`
	Preview     *string `json:"preview"`
	Shares      int64   `json:"shares"`
	// "tool" or "auto", and who sent the file (auto-saved ones).
	Origin     string  `json:"origin"`
	SenderName *string `json:"senderName"`
	SentAt     *int64  `json:"sentAt"`
}

// senderOf names who sent an auto-saved file: their name, else their id.
func senderOf(a db.AttachmentMeta) *string {
	if a.Origin != attachments.OriginAuto {
		return nil
	}
	if a.SenderName != nil && *a.SenderName != "" {
		return a.SenderName
	}
	return a.SenderID
}

func init() {
	// Auto-save for one chat: on or off, and how many days files stay (0 keeps
	// them). Turning it off keeps what was saved.
	httpapi.Register(httpapi.Handler{
		Name:   "setChatSaveMediaFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("chatId", chatIDArg), zod.F("on", zod.Optional(zod.Bool())), zod.F("days", zod.Optional(zod.Int().Min(0).Max(365)))),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			chatID, _ := o.Get("chatId")
			var patch chats.SaveMediaPatch
			if v, ok := o.Get("on"); ok {
				b := v.(bool)
				patch.On = &b
			}
			if v, ok := o.Get("days"); ok {
				days, _ := v.(float64)
				if !attachments.ValidAutoDays(int64(days)) {
					return nil, httpapi.Fail("Pick 7, 30, 90 or 365 days, or keep files until deleted.")
				}
				n := int64(days)
				patch.Days = &n
			}
			_, ok, err := chats.SetSaveMedia(c.DB(), chatID.(string), patch)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, httpapi.Fail(chatGone)
			}
			return okResult{OK: true}, nil
		},
	})

	// Deletes every auto-saved file of a chat (and their links). Files saved
	// with save_attachment stay.
	httpapi.Register(httpapi.Handler{
		Name:   "deleteAutoSavedFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("chatId", chatIDArg)),
		Fn: func(c *httpapi.Call) (any, error) {
			chatID, _ := c.Obj().Get("chatId")
			n, err := attachments.DeleteAuto(c.DB(), chatID.(string))
			if err != nil {
				return nil, err
			}
			return struct {
				OK      bool  `json:"ok"`
				Deleted int64 `json:"deleted"`
			}{true, n}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "getAttachments",
		Method: http.MethodGet,
		Guard:  httpapi.User,
		Fn: func(c *httpapi.Call) (any, error) {
			q := c.DB()
			titles, err := chatTitles(q)
			if err != nil {
				return nil, err
			}
			rows, err := attachments.List(q, attachments.ListOptions{Limit: 500})
			if err != nil {
				return nil, err
			}
			shares, err := attachments.ActiveShareCounts(q, c.Now())
			if err != nil {
				return nil, err
			}
			list := make([]attachmentItem, 0, len(rows))
			for _, a := range rows {
				chat, ok := titles[a.ChatID]
				if !ok {
					chat = a.ChatID
				}
				var preview *string
				if p := attachments.PreviewKind(a.MimeType); p != "" {
					preview = &p
				}
				list = append(list, attachmentItem{ID: a.ID, ChatID: a.ChatID, Title: a.Title, Description: a.Description, Kind: a.Kind, MimeType: a.MimeType, Filename: a.Filename, Size: a.Size, CreatedAt: a.CreatedAt, Chat: chat, Preview: preview, Shares: shares[a.ID],
					Origin: a.Origin, SenderName: senderOf(a), SentAt: a.SentAt})
			}
			tz, err := settings.Timezone(q)
			if err != nil {
				return nil, err
			}
			return struct {
				Attachments []attachmentItem `json:"attachments"`
				Timezone    string           `json:"timezone"`
			}{list, tz}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "deleteAttachmentFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("id", attachmentID)),
		Fn: func(c *httpapi.Call) (any, error) {
			id, _ := c.Obj().Get("id")
			ok, err := attachments.Delete(c.DB(), id.(string))
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, httpapi.Fail(fileGone)
			}
			return okResult{OK: true}, nil
		},
	})

	// Live links for one file. The link itself isn't here: only its hash is stored.
	httpapi.Register(httpapi.Handler{
		Name:   "getAttachmentShares",
		Method: http.MethodGet,
		Guard:  httpapi.User,
		Input:  zod.Object(zod.F("id", attachmentID)),
		Fn: func(c *httpapi.Call) (any, error) {
			id, _ := c.Obj().Get("id")
			list, err := attachments.ListActiveShares(c.DB(), id.(string), c.Now())
			if err != nil {
				return nil, err
			}
			if list == nil {
				list = []attachments.ShareView{}
			}
			return struct {
				Shares []attachments.ShareView `json:"shares"`
			}{list}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "createAttachmentShareFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("id", attachmentID), zod.F("visibility", zod.Enum("public", "private")), zod.F("duration", zod.Enum(attachments.ShareDurationKeys...))),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			id, _ := o.Get("id")
			visibility, _ := o.Get("visibility")
			duration, _ := o.Get("duration")
			if _, ok, err := attachments.Get(c.DB(), id.(string)); err != nil {
				return nil, err
			} else if !ok {
				return nil, httpapi.Fail(fileGone)
			}
			made, ok, err := attachments.CreateShare(c.DB(), attachments.ShareInput{AttachmentID: id.(string), Visibility: visibility.(string), Duration: duration.(string), CreatedBy: "owner"}, c.Now())
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, httpapi.Fail(fileGone)
			}
			return struct {
				Share attachments.ShareView `json:"share"`
				URL   string                `json:"url"`
			}{made.Share, attachments.ShareURL(c.AppURL(), made.Token)}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "revokeAttachmentShareFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("shareId", zod.String().Min(1).Max(100))),
		Fn: func(c *httpapi.Call) (any, error) {
			id, _ := c.Obj().Get("shareId")
			if _, err := attachments.RevokeShare(c.DB(), id.(string), c.Now()); err != nil {
				return nil, err
			}
			return okResult{OK: true}, nil
		},
	})
}
