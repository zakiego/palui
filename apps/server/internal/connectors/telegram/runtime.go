package telegram

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/channelutil"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/outmedia"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	Limit                 = 4096
	CallbackDataMaxBytes  = 64
	DocumentMaxBytes      = 1024 * 1024
	DefaultTypingInterval = 4000
	DefaultTypingMax      = 10 * 60_000
	// pollTimeoutSeconds is grammY's long polling timeout.
	pollTimeoutSeconds = 30
)

// Bot-facing copy, verbatim.
const (
	CopyClaimed     = "Halo! Kamu sekarang owner Palui."
	CopyClaimFailed = "Link-nya udah kedaluwarsa atau udah dipakai. Bikin link baru dari dashboard."
)

// Owner-facing errors.
const (
	ErrMissingToken = "Bot token is missing. Paste it from @BotFather."
	ErrUnauthorized = "Telegram rejected the bot token (401 Unauthorized). Copy it again from @BotFather."
	ErrConflict     = "Telegram says another process is already polling this bot token (409 Conflict). Stop the other one, or give dev its own bot in @BotFather."
	ErrDevGuard     = "This is the prod bot token, and dev never polls with it (it would knock prod off with 409s). Create a separate dev bot in @BotFather and paste that token."
	// The polling loop ended without a stop.
	ErrPollingStopped = "Telegram polling stopped unexpectedly"
)

// Options are the TS TelegramDeps. Zero values are the defaults.
type Options struct {
	// createBot: the transport for a token (default the real Bot API).
	NewTransport func(token string) Transport
	// Downloads a file from Telegram's file endpoint.
	FetchFile        func(ctx context.Context, url string) ([]byte, error)
	TypingIntervalMs int64
	// Safety cap: a typing loop nobody switched off stops by itself.
	TypingMaxMs int64
	Timers      channelutil.Timers
	Now         func() int64
	// Sleep between polling retries (grammY's sleep); nil waits for real.
	Sleep func(ctx context.Context, ms int64) error
}

func (o Options) withDefaults() Options {
	if o.NewTransport == nil {
		o.NewTransport = NewHTTPTransport
	}
	if o.FetchFile == nil {
		o.FetchFile = defaultFetchFile
	}
	if o.TypingIntervalMs == 0 {
		o.TypingIntervalMs = DefaultTypingInterval
	}
	if o.TypingMaxMs == 0 {
		o.TypingMaxMs = DefaultTypingMax
	}
	if o.Timers == nil {
		o.Timers = channelutil.RealTimers{}
	}
	if o.Now == nil {
		o.Now = func() int64 { return time.Now().UnixMilli() }
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, ms int64) error {
			t := time.NewTimer(time.Duration(ms) * time.Millisecond)
			defer t.Stop()
			select {
			case <-t.C:
				return nil
			case <-ctx.Done():
				return errors.New("Aborted delay")
			}
		}
	}
	return o
}

func defaultFetchFile(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("file download failed: HTTP %d", res.StatusCode)
	}
	return io.ReadAll(res.Body)
}

// DescribeError is describeTelegramError.
func DescribeError(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		switch api.Code {
		case 401:
			return ErrUnauthorized
		case 409:
			return ErrConflict
		}
		return fmt.Sprintf("Telegram error %d: %s", api.Code, api.Description)
	}
	var h *HTTPError
	if errors.As(err, &h) {
		return "Can't reach Telegram: " + h.Error()
	}
	if err == nil {
		return "undefined"
	}
	return err.Error()
}

// IsProdToken is isProdToken: dev mode refuses tokens whose SHA-256 is in
// PALUI_PROD_TOKEN_HASHES.
func IsProdToken(token string, cfg config.Config) bool {
	if cfg.Mode != "dev" || len(cfg.ProdTokenHashes) == 0 {
		return false
	}
	hash := secret.Sha256(token)
	for _, h := range cfg.ProdTokenHashes {
		if strings.ToLower(h) == hash {
			return true
		}
	}
	return false
}

// ValidCallbackData is 1 to 64 bytes of UTF-8.
func ValidCallbackData(data string) bool {
	n := len(data)
	return n >= 1 && n <= CallbackDataMaxBytes
}

// BuildKeyboard is buildKeyboard: rows of 3 buttons, invalid ids dropped
// (onInvalid hears each one). Nil when nothing is left.
func BuildKeyboard(actions []db.OutboxAction, onInvalid func(id string)) *jsonx.Object {
	var valid []db.OutboxAction
	for _, a := range actions {
		if ValidCallbackData(a.ID) {
			valid = append(valid, a)
		} else if onInvalid != nil {
			onInvalid(a.ID)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	rows := []any{}
	for i := 0; i < len(valid); i += 3 {
		row := []any{}
		for _, a := range valid[i:min(i+3, len(valid))] {
			row = append(row, obj("text", a.Label, "callback_data", a.ID))
		}
		rows = append(rows, row)
	}
	return obj("inline_keyboard", rows)
}

// RenderedChunk is one message: HTML and the markdown it came from.
type RenderedChunk struct {
	HTML  string `json:"html"`
	Plain string `json:"plain"`
}

// RenderChunks is renderTelegramChunks: markdown split first (so each chunk
// is valid on its own), then converted. A chunk whose HTML still runs over the
// limit is split again with a smaller budget.
func RenderChunks(md string, limit int) []RenderedChunk {
	var out []RenderedChunk
	var gofn func(text string, budget int)
	gofn = func(text string, budget int) {
		for _, plain := range channelutil.SplitMessage(text, budget) {
			html := channelutil.MarkdownToTelegramHTML(plain)
			if strx.Len16(html) <= limit || budget <= 256 {
				out = append(out, RenderedChunk{HTML: html, Plain: plain})
			} else {
				gofn(plain, int(math.Floor(float64(budget)*0.6)))
			}
		}
	}
	gofn(md, limit)
	return out
}

var parseErrRe = regexp.MustCompile(`(?i)parse|entit|tag`)

func isParseError(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Code == 400 && parseErrRe.MatchString(api.Description)
}

func displayName(u User) string {
	var parts []string
	for _, p := range []string{u.FirstName, u.LastName} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if s := strings.Join(parts, " "); s != "" {
		return s
	}
	if u.Username != "" {
		return u.Username
	}
	return strconv.FormatInt(u.ID, 10)
}

// startNonce is START_RE = /^\/start(?:@\w+)?(?:\s+(\S+))?\s*$/: the nonce,
// or ok false when the text isn't a /start with one.
func startNonce(text string) (string, bool) {
	if !strings.HasPrefix(text, "/start") {
		return "", false
	}
	rest := []rune(text[len("/start"):])
	if len(rest) > 0 && rest[0] == '@' {
		j := 1
		for j < len(rest) && isWordRune(rest[j]) {
			j++
		}
		if j > 1 {
			rest = rest[j:]
		}
	}
	i := 0
	for i < len(rest) && strx.IsSpace(rest[i]) {
		i++
	}
	if i == len(rest) || i == 0 {
		return "", false
	}
	j := i
	for j < len(rest) && !strx.IsSpace(rest[j]) {
		j++
	}
	k := j
	for k < len(rest) && strx.IsSpace(rest[k]) {
		k++
	}
	if k != len(rest) {
		return "", false
	}
	return string(rest[i:j]), true
}

func isWordRune(r rune) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// New is createTelegramRuntime(overrides, m): m is the main Telegram
// manifest, or tgbot for an agent's own bot (PLAN 19.6).
func New(o Options, m connectors.Manifest) *connectors.Runtime {
	o = o.withDefaults()
	return &connectors.Runtime{
		Manifest: m,
		Test: func(c connectors.Context) (connectors.TestResult, error) {
			token := c.Secret("botToken")
			if token == "" {
				return connectors.TestResult{OK: false, Detail: ErrMissingToken}, nil
			}
			if IsProdToken(token, config.Get()) {
				return connectors.TestResult{OK: false, Detail: ErrDevGuard}, nil
			}
			me, err := getMe(c.Ctx, o.NewTransport(token))
			if err != nil {
				return connectors.TestResult{OK: false, Detail: DescribeError(err)}, nil
			}
			return connectors.TestResult{OK: true, Detail: "@" + me.Username + " reachable", Data: obj("username", me.Username)}, nil
		},
		Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
			return start(c, o)
		},
	}
}

// Runtime is the main Telegram connector.
var Runtime = New(Options{}, Manifest)

type typingLoop struct {
	interval any
	cap      any
}

type bot struct {
	c     connectors.Context
	o     Options
	log   *logx.Logger
	api   Transport
	token string
	botID int64

	mu       sync.Mutex
	stopping bool
	running  bool
	username string
	typing   map[string]*typingLoop
	lastTry  int64

	pollCtx    context.Context
	pollCancel context.CancelFunc
	done       chan struct{}
	stopOnce   sync.Once
}

func start(c connectors.Context, o Options) (*connectors.RunningConnector, error) {
	token := c.Secret("botToken")
	if token == "" {
		detail := ErrMissingToken
		c.SetStatus("error", &detail)
		return nil, errors.New(ErrMissingToken)
	}
	if IsProdToken(token, config.Get()) {
		detail := ErrDevGuard
		c.SetStatus("error", &detail)
		return nil, errors.New(ErrDevGuard)
	}
	log := c.Log
	if log == nil {
		log = logx.Silent
	}
	b := &bot{
		c: c, o: o, token: token,
		log:    log.Child(map[string]any{"connector_id": c.ID}),
		api:    o.NewTransport(token),
		typing: map[string]*typingLoop{},
		done:   make(chan struct{}),
	}
	// The bot's own user id is the token's prefix.
	b.botID = int64(strx.Number(strings.SplitN(token, ":", 2)[0]))
	if v, ok := c.Config.Get("botUsername"); ok {
		if s, ok := v.(string); ok {
			b.username = s
		}
	}
	b.pollCtx, b.pollCancel = context.WithCancel(context.Background())

	c.SetStatus("connecting", nil)
	b.running = true
	go b.poll()
	if c.Ctx != nil {
		go func() {
			select {
			case <-c.Ctx.Done():
				b.stop()
			case <-b.done:
			}
		}()
	}

	return &connectors.RunningConnector{
		Stop:    func(context.Context) error { b.stop(); return nil },
		Channel: b,
		Live:    b.live,
		Action:  b.action,
	}, nil
}

func (b *bot) learnUsername(name string) {
	b.mu.Lock()
	if name == "" || name == b.username {
		b.mu.Unlock()
		return
	}
	b.username = name
	b.mu.Unlock()
	b.c.PatchConfig(obj("botUsername", name))
}

func (b *bot) isRunning() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}

// --- polling (grammY bot.start, simple long polling) ------------------------------------

// withRetries is grammY's withRetries: network errors and 5xx retry with a
// doubling delay (none before the first retry), 429 waits retry_after, other
// errors are thrown.
func (b *bot) withRetries(ctx context.Context, task func() error) error {
	const initial = 50
	last := int64(initial)
	for {
		err := task()
		if err == nil {
			return nil
		}
		var api *APIError
		var h *HTTPError
		delay := false
		switch {
		case errors.As(err, &h):
			delay = true
		case errors.As(err, &api) && api.Code >= 500:
			delay = true
		case errors.As(err, &api) && api.Code == 429:
			if api.RetryAfter > 0 {
				if serr := b.o.Sleep(ctx, int64(api.RetryAfter)*1000); serr != nil {
					return serr
				}
				last = initial
			} else {
				delay = true
			}
		default:
			return err
		}
		if delay {
			if last != initial {
				if serr := b.o.Sleep(ctx, last); serr != nil {
					return serr
				}
			}
			last = min(20*60*1000, 2*last)
		}
	}
}

func (b *bot) poll() {
	defer close(b.done)
	err := b.runPolling()
	b.mu.Lock()
	b.running = false
	stopping := b.stopping
	b.mu.Unlock()
	if stopping {
		return
	}
	if err != nil {
		detail := DescribeError(err)
		b.log.Error("telegram polling failed", map[string]any{"err": detail})
		b.c.SetStatus("error", &detail)
		return
	}
	detail := ErrPollingStopped
	b.c.SetStatus("error", &detail)
}

func (b *bot) runPolling() error {
	ctx := b.pollCtx
	var me User
	if err := b.withRetries(ctx, func() error {
		var err error
		me, err = getMe(ctx, b.api)
		return err
	}); err != nil {
		return err
	}
	if err := b.withRetries(ctx, func() error {
		_, err := b.api.Call(ctx, "deleteWebhook", jsonx.NewMap[any]())
		return err
	}); err != nil {
		return err
	}
	b.learnUsername(me.Username)
	b.log.Info("telegram polling started", map[string]any{"username": me.Username})
	b.c.SetStatus("on", nil)
	if !b.isRunning() {
		return nil
	}
	allowed := []any{"message", "callback_query"}
	for b.isRunning() {
		updates, err := b.fetchUpdates(ctx, allowed)
		if err != nil {
			return err
		}
		if updates == nil {
			break
		}
		for _, u := range updates {
			b.mu.Lock()
			b.lastTry = u.UpdateID
			b.mu.Unlock()
			b.handleUpdate(u)
		}
		// Telegram keeps the last allowed_updates, so it is only sent once.
		allowed = nil
	}
	return nil
}

// fetchUpdates is grammY's fetchUpdates: nil when the bot stopped.
func (b *bot) fetchUpdates(ctx context.Context, allowed []any) ([]Update, error) {
	for {
		b.mu.Lock()
		offset := b.lastTry + 1
		b.mu.Unlock()
		p := obj("offset", offset, "timeout", pollTimeoutSeconds)
		if allowed != nil {
			p.Set("allowed_updates", allowed)
		}
		updates, err := call[[]Update](ctx, b.api, "getUpdates", p)
		if err == nil {
			if updates == nil {
				updates = []Update{}
			}
			return updates, nil
		}
		if !b.isRunning() {
			return nil, nil
		}
		// handlePollingError: 401 and 409 are fatal, 429 waits retry_after.
		wait := int64(3)
		var api *APIError
		if errors.As(err, &api) {
			if api.Code == 401 || api.Code == 409 {
				return nil, err
			}
			if api.Code == 429 && api.RetryAfter > 0 {
				wait = int64(api.RetryAfter)
			}
		}
		b.log.Debug("Call to getUpdates failed, retrying", map[string]any{"err": err.Error(), "seconds": wait})
		_ = b.o.Sleep(ctx, wait*1000)
		if !b.isRunning() {
			return nil, nil
		}
	}
}

func (b *bot) handleUpdate(u Update) {
	defer func() {
		if p := recover(); p != nil {
			b.log.Error("telegram update handler failed", map[string]any{"err": fmt.Sprint(p)})
		}
	}()
	var err error
	switch {
	case u.Message != nil:
		err = b.onMessage(u.Message)
	case u.CallbackQuery != nil && u.CallbackQuery.Data != nil:
		err = b.onCallback(u.CallbackQuery)
	}
	if err != nil {
		b.log.Error("telegram update handler failed", map[string]any{"err": err.Error()})
	}
}

func (b *bot) stop() {
	b.stopOnce.Do(func() {
		b.mu.Lock()
		b.stopping = true
		ids := make([]string, 0, len(b.typing))
		for id := range b.typing {
			ids = append(ids, id)
		}
		b.mu.Unlock()
		for _, id := range ids {
			b.stopTyping(id)
		}
		b.mu.Lock()
		wasRunning := b.running
		b.running = false
		offset := b.lastTry + 1
		b.mu.Unlock()
		if wasRunning {
			b.pollCancel()
			// Confirm the last update so it isn't fetched again (grammY bot.stop).
			if _, err := b.api.Call(context.Background(), "getUpdates", obj("offset", offset, "limit", 1)); err != nil {
				b.log.Debug("bot.stop failed", map[string]any{"err": DescribeError(err)})
			}
		} else {
			b.pollCancel()
		}
		<-b.done
	})
}

// --- inbound ----------------------------------------------------------------------------

func (b *bot) download(fileID string) []byte {
	f, err := call[File](b.c.Ctx, b.api, "getFile", obj("file_id", fileID))
	if err == nil && f.FilePath == "" {
		return nil
	}
	var data []byte
	if err == nil {
		data, err = b.o.FetchFile(b.c.Ctx, APIRoot+"/file/bot"+b.token+"/"+f.FilePath)
	}
	if err != nil {
		b.log.Warn("telegram file download failed", map[string]any{"err": DescribeError(err)})
		return nil
	}
	return data
}

// hasMedia: the message carries a photo, voice note or document.
func hasMedia(msg *Message) bool {
	return len(msg.Photo) > 0 || msg.Voice != nil || msg.Document != nil
}

// mediaOf downloads the media only when wanted (see Services.WantsMedia),
// or when the chat auto-saves it (save, photos and documents only; see
// Services.SavesMedia). Otherwise it returns the media without bytes, so a
// photo with no caption still reaches the inbox (group context shows it as a
// placeholder). Media fetched only for the auto-save is SaveOnly and adds no note.
func (b *bot) mediaOf(msg *Message, wanted, save bool) ([]connectors.InboundMedia, string) {
	caption := ""
	if msg.Caption != nil {
		caption = *msg.Caption
	}
	failed := func(stub connectors.InboundMedia, kind string) ([]connectors.InboundMedia, string) {
		if !wanted {
			return []connectors.InboundMedia{stub}, ""
		}
		return nil, fmt.Sprintf("[%s could not be downloaded]", kind)
	}
	if len(msg.Photo) > 0 {
		stub := connectors.InboundMedia{Kind: "image", MimeType: "image/jpeg"}
		if !wanted && !save {
			return []connectors.InboundMedia{stub}, ""
		}
		largest := msg.Photo[0]
		for _, p := range msg.Photo[1:] {
			if p.Width*p.Height > largest.Width*largest.Height {
				largest = p
			}
		}
		data := b.download(largest.FileID)
		if data == nil {
			return failed(stub, "image")
		}
		stub.Data = base64.StdEncoding.EncodeToString(data)
		stub.SaveOnly = !wanted
		stub.Caption = caption
		return []connectors.InboundMedia{stub}, ""
	}
	if msg.Voice != nil {
		mime := "audio/ogg"
		if msg.Voice.MimeType != nil {
			mime = *msg.Voice.MimeType
		}
		d := msg.Voice.Duration
		stub := connectors.InboundMedia{Kind: "audio", MimeType: mime, DurationSec: &d}
		if !wanted {
			return []connectors.InboundMedia{stub}, ""
		}
		data := b.download(msg.Voice.FileID)
		if data == nil {
			return failed(stub, "audio")
		}
		stub.Data = base64.StdEncoding.EncodeToString(data)
		return []connectors.InboundMedia{stub}, ""
	}
	if msg.Document != nil {
		doc := msg.Document
		name := "document"
		if doc.FileName != nil {
			name = *doc.FileName
		}
		mime := "application/octet-stream"
		if doc.MimeType != nil {
			mime = *doc.MimeType
		}
		stub := connectors.InboundMedia{Kind: "document", MimeType: mime, Filename: name}
		if !wanted && !save {
			return []connectors.InboundMedia{stub}, ""
		}
		limit := float64(DocumentMaxBytes)
		if save {
			limit = attachments.MaxBytes
		}
		size := 0.0
		if doc.FileSize != nil {
			size = *doc.FileSize
		}
		if size > limit {
			if !wanted {
				return []connectors.InboundMedia{stub}, ""
			}
			mb := strx.ToFixed(size/1024/1024, 1)
			return nil, fmt.Sprintf("[document %s (%s MB) is over the 1 MB limit and was not read]", name, mb)
		}
		data := b.download(doc.FileID)
		if data == nil {
			return failed(stub, "document")
		}
		if float64(len(data)) > limit {
			if !wanted {
				return []connectors.InboundMedia{stub}, ""
			}
			return nil, fmt.Sprintf("[document %s is over the 1 MB limit and was not read]", name)
		}
		stub.Data = base64.StdEncoding.EncodeToString(data)
		stub.Caption = caption
		stub.SaveOnly = !wanted
		if wanted && len(data) > DocumentMaxBytes {
			// Kept for the auto-save only; the model can't take it.
			stub.SaveOnly = true
			return []connectors.InboundMedia{stub}, fmt.Sprintf("[document %s is over the 1 MB limit and was not read]", name)
		}
		return []connectors.InboundMedia{stub}, ""
	}
	return nil, ""
}

// addressed: a group message meant for this bot (PLAN 19.2). It @mentions the
// bot, runs one of its commands, or replies to one of its messages.
func (b *bot) addressed(msg *Message) bool {
	if r := msg.ReplyToMessage; r != nil && r.From != nil && r.From.ID == b.botID {
		return true
	}
	text := ""
	if msg.Text != nil {
		text = *msg.Text
	} else if msg.Caption != nil {
		text = *msg.Caption
	}
	b.mu.Lock()
	me := ""
	if b.username != "" {
		me = "@" + strx.Lower(b.username)
	}
	b.mu.Unlock()
	var entities []Entity
	if msg.Entities != nil {
		entities = *msg.Entities
	} else if msg.CaptionEntities != nil {
		entities = *msg.CaptionEntities
	}
	for _, e := range entities {
		if e.Type == "text_mention" && e.User != nil && e.User.ID == b.botID {
			return true
		}
		part := strx.Lower(strx.Slice16Range(text, e.Offset, e.Offset+e.Length))
		if me != "" && e.Type == "mention" && part == me {
			return true
		}
		if me != "" && e.Type == "bot_command" && strings.HasSuffix(part, me) {
			return true
		}
	}
	return false
}

func (b *bot) reply(msg *Message, text string) error {
	p := obj("chat_id", msg.Chat.ID, "text", text)
	if msg.IsTopicMessage && msg.MessageThreadID != nil {
		p.Set("message_thread_id", *msg.MessageThreadID)
	}
	if msg.DirectTopic != nil {
		p.Set("direct_messages_topic_id", msg.DirectTopic.TopicID)
	}
	_, err := b.api.Call(b.c.Ctx, "sendMessage", p)
	return err
}

func (b *bot) onMessage(msg *Message) error {
	from := msg.From
	if from == nil || from.IsBot {
		return nil
	}
	text := ""
	if msg.Text != nil {
		text = *msg.Text
	} else if msg.Caption != nil {
		text = *msg.Caption
	}

	if msg.Text != nil {
		if nonce, ok := startNonce(*msg.Text); ok && nonce != "" {
			ok, err := b.c.Services.ClaimOwner(b.c.Ctx, connectors.ClaimInput{Channel: "telegram", Nonce: nonce, ExternalID: strconv.FormatInt(from.ID, 10), DisplayName: displayName(*from)})
			if err != nil {
				return err
			}
			if ok {
				b.c.Event("owner_claimed", fmt.Sprintf("Telegram owner claimed by %d", from.ID), "info", nil)
				return b.reply(msg, CopyClaimed)
			}
			b.c.Event("owner_claim_failed", fmt.Sprintf("Telegram claim refused for %d", from.ID), "warn", nil)
			return b.reply(msg, CopyClaimFailed)
		}
	}

	if text == "" && !hasMedia(msg) {
		b.log.Debug("telegram message without supported content dropped", map[string]any{"chat_id": msg.Chat.ID})
		return nil
	}

	in := connectors.InboundMessage{
		Channel:          "telegram",
		ConnectorID:      b.c.ID,
		ChatExternalID:   strconv.FormatInt(msg.Chat.ID, 10),
		ChatKind:         "group",
		SenderExternalID: strconv.FormatInt(from.ID, 10),
		SenderName:       displayName(*from),
		ChannelMsgID:     strconv.FormatInt(msg.MessageID, 10),
		Text:             text,
		At:               msg.Date * 1000,
	}
	if msg.Chat.Type == "private" {
		in.ChatKind = "dm"
		in.ChatTitle = displayName(*from)
	} else {
		in.ChatTitle = msg.Chat.Title
	}
	if msg.ReplyToMessage != nil {
		in.ReplyToChannelMsgID = strconv.FormatInt(msg.ReplyToMessage.MessageID, 10)
	}
	if in.ChatKind == "group" {
		in.Addressed = b.addressed(msg)
	}
	if hasMedia(msg) {
		wanted := false
		if b.c.Services.WantsMedia != nil {
			wanted = b.c.Services.WantsMedia(in)
		} else {
			wanted = b.c.Services.IsOwner("telegram", in.SenderExternalID, "")
		}
		save := len(msg.Photo) > 0 || msg.Document != nil
		save = save && b.c.Services.SavesMedia != nil && b.c.Services.SavesMedia(in)
		media, note := b.mediaOf(msg, wanted, save)
		if note != "" {
			in.Text = strings.TrimPrefix(in.Text+"\n"+note, "\n")
		}
		if len(media) > 0 {
			in.Media = media
		}
	}
	return b.c.Services.Inbound(b.c.Ctx, in)
}

func (b *bot) onCallback(q *CallbackQuery) error {
	chat := (*Chat)(nil)
	if q.Message != nil {
		chat = &q.Message.Chat
	}
	in := connectors.InboundMessage{
		Channel:          "telegram",
		ConnectorID:      b.c.ID,
		ChatExternalID:   strconv.FormatInt(q.From.ID, 10),
		ChatKind:         "dm",
		SenderExternalID: strconv.FormatInt(q.From.ID, 10),
		SenderName:       displayName(q.From),
		Text:             "",
		Callback:         &connectors.InboundCallback{Data: *q.Data, CallbackID: q.ID},
		At:               b.o.Now(),
	}
	if chat != nil {
		in.ChatExternalID = strconv.FormatInt(chat.ID, 10)
		if chat.Type != "private" {
			in.ChatKind = "group"
		}
	}
	if q.Message != nil {
		in.ReplyToChannelMsgID = strconv.FormatInt(q.Message.MessageID, 10)
	}
	if err := b.c.Services.Inbound(b.c.Ctx, in); err != nil {
		return err
	}
	// Stop the spinner on the button. The router can't add a toast afterwards:
	// Telegram accepts one answer per query.
	return b.AnswerCallback(b.c.Ctx, q.ID, "")
}

// --- channel handle -------------------------------------------------------------------

func (b *bot) Channel() string { return "telegram" }

func (b *bot) Reachable() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.stopping && b.running
}

// AnswerCallback answers a button press (errors are only logged).
func (b *bot) AnswerCallback(ctx context.Context, callbackID, text string) error {
	p := obj("callback_query_id", callbackID)
	if text != "" {
		p.Set("text", text)
	}
	if _, err := b.api.Call(ctx, "answerCallbackQuery", p); err != nil {
		b.log.Debug("answerCallbackQuery failed", map[string]any{"err": DescribeError(err)})
	}
	return nil
}

func (b *bot) stopTyping(chatID string) {
	b.mu.Lock()
	loop := b.typing[chatID]
	delete(b.typing, chatID)
	b.mu.Unlock()
	if loop == nil {
		return
	}
	b.o.Timers.ClearInterval(loop.interval)
	b.o.Timers.ClearTimeout(loop.cap)
}

// Typing repeats sendChatAction every 4 s until switched off, a reply goes
// out, the connector stops or the 10 minute cap.
func (b *bot) Typing(_ context.Context, chatID string, on bool, _ connectors.TypingOptions) error {
	b.stopTyping(chatID)
	b.mu.Lock()
	stopping := b.stopping
	b.mu.Unlock()
	if !on || stopping {
		return nil
	}
	tick := func() {
		go func() {
			if _, err := b.api.Call(context.Background(), "sendChatAction", obj("chat_id", chatID, "action", "typing")); err != nil {
				b.log.Debug("sendChatAction failed", map[string]any{"err": DescribeError(err)})
			}
		}()
	}
	tick()
	loop := &typingLoop{}
	loop.interval = b.o.Timers.SetInterval(tick, b.o.TypingIntervalMs)
	loop.cap = b.o.Timers.SetTimeout(func() { b.stopTyping(chatID) }, b.o.TypingMaxMs)
	b.mu.Lock()
	b.typing[chatID] = loop
	b.mu.Unlock()
	return nil
}

// Send splits, converts and sends one outbox payload; the keyboard rides on
// the last chunk, the reply target on the first.
func (b *bot) Send(ctx context.Context, chatID string, payload db.OutboxPayload) (connectors.SendResult, error) {
	if payload.Media != nil {
		return b.sendMedia(ctx, chatID, payload)
	}
	b.stopTyping(chatID)
	var chunks []RenderedChunk
	if payload.Format == "plain" {
		for _, t := range channelutil.SplitMessage(payload.Text, Limit) {
			chunks = append(chunks, RenderedChunk{Plain: t})
		}
	} else {
		chunks = RenderChunks(payload.Text, Limit)
	}
	if len(chunks) == 0 {
		chunks = append(chunks, RenderedChunk{Plain: " "})
	}
	keyboard := BuildKeyboard(payload.Actions, func(id string) {
		b.log.Warn("telegram button dropped: callback_data must be 1 to 64 bytes", map[string]any{"id": id})
	})
	replyTo := math.NaN()
	if payload.ReplyToChannelMsgID != "" {
		replyTo = strx.Number(payload.ReplyToChannelMsgID)
	}
	isInt := !math.IsNaN(replyTo) && !math.IsInf(replyTo, 0) && replyTo == math.Trunc(replyTo)
	var lastID *int64
	for i, chunk := range chunks {
		other := jsonx.NewMap[any]()
		if i == 0 && isInt {
			other.Set("reply_parameters", obj("message_id", replyTo, "allow_sending_without_reply", true))
		}
		if i == len(chunks)-1 && keyboard != nil {
			other.Set("reply_markup", keyboard)
		}
		var sent Message
		var err error
		if chunk.HTML != "" {
			sent, err = b.sendMessage(ctx, chatID, chunk.HTML, other, true)
			if err != nil {
				if !isParseError(err) {
					return connectors.SendResult{}, err
				}
				b.log.Warn("telegram rejected HTML, sending plain text", map[string]any{"err": DescribeError(err)})
				sent, err = b.sendMessage(ctx, chatID, chunk.Plain, other, false)
			}
		} else {
			sent, err = b.sendMessage(ctx, chatID, chunk.Plain, other, false)
		}
		if err != nil {
			return connectors.SendResult{}, err
		}
		id := sent.MessageID
		lastID = &id
	}
	if lastID == nil {
		return connectors.SendResult{}, nil
	}
	return connectors.SendResult{ChannelMsgID: strconv.FormatInt(*lastID, 10)}, nil
}

// mediaMethods is the Bot API method and file field per media kind.
var mediaMethods = map[string][2]string{
	db.MediaImage:    {"sendPhoto", "photo"},
	db.MediaVideo:    {"sendVideo", "video"},
	db.MediaVoice:    {"sendVoice", "voice"},
	db.MediaAudio:    {"sendAudio", "audio"},
	db.MediaSticker:  {"sendSticker", "sticker"},
	db.MediaDocument: {"sendDocument", "document"},
}

// sendMedia uploads one photo, file, voice note, video or sticker with its
// caption. Typing stays on: media goes out mid-turn. When Telegram refuses a
// file as its kind (a photo too wide, voice notes off for the user), it goes
// again as a document.
func (b *bot) sendMedia(ctx context.Context, chatID string, payload db.OutboxPayload) (connectors.SendResult, error) {
	m := payload.Media
	if len(m.Bytes) == 0 {
		return connectors.SendResult{}, errors.New("media bytes missing")
	}
	kind := m.Kind
	if _, ok := mediaMethods[kind]; !ok {
		kind = db.MediaDocument
	}
	other := jsonx.NewMap[any]()
	if payload.ReplyToChannelMsgID != "" {
		if id := strx.Number(payload.ReplyToChannelMsgID); !math.IsNaN(id) && !math.IsInf(id, 0) && id == math.Trunc(id) {
			other.Set("reply_parameters", obj("message_id", id, "allow_sending_without_reply", true))
		}
	}
	if keyboard := BuildKeyboard(payload.Actions, func(id string) {
		b.log.Warn("telegram button dropped: callback_data must be 1 to 64 bytes", map[string]any{"id": id})
	}); keyboard != nil {
		other.Set("reply_markup", keyboard)
	}
	plain := strx.Slice16(strx.Trim(payload.Text), outmedia.MaxCaption)
	html := ""
	if payload.Format != "plain" && plain != "" {
		if chunks := RenderChunks(payload.Text, outmedia.MaxCaption); len(chunks) == 1 {
			html = chunks[0].HTML
		}
	}
	name := m.Filename
	if name == "" {
		name = "file"
	}
	file := &InputFile{Name: name, MimeType: m.MimeType, Bytes: m.Bytes}
	send := func(kind string, caption string, asHTML bool) (Message, error) {
		mm := mediaMethods[kind]
		p := obj("chat_id", chatID, mm[1], file)
		if caption != "" && kind != db.MediaSticker {
			p.Set("caption", caption)
			if asHTML {
				p.Set("parse_mode", "HTML")
			}
		}
		p.Assign(other)
		return call[Message](ctx, b.api, mm[0], p)
	}
	try := func(kind string) (Message, error) {
		if html != "" {
			sent, err := send(kind, html, true)
			if err == nil || !isParseError(err) {
				return sent, err
			}
			b.log.Warn("telegram rejected the caption HTML, sending it plain", map[string]any{"err": DescribeError(err)})
		}
		return send(kind, plain, false)
	}
	sent, err := try(kind)
	var api *APIError
	if err != nil && kind != db.MediaDocument && errors.As(err, &api) && api.Code == 400 {
		b.log.Warn("telegram refused the file as "+kind+", sending it as a document", map[string]any{"err": DescribeError(err)})
		sent, err = try(db.MediaDocument)
	}
	if err != nil {
		return connectors.SendResult{}, err
	}
	return connectors.SendResult{ChannelMsgID: strconv.FormatInt(sent.MessageID, 10)}, nil
}

func (b *bot) sendMessage(ctx context.Context, chatID, text string, other *jsonx.Object, html bool) (Message, error) {
	p := obj("chat_id", chatID, "text", text)
	p.Assign(other)
	if html {
		p.Set("parse_mode", "HTML")
	}
	return call[Message](ctx, b.api, "sendMessage", p)
}

// --- live and actions ---------------------------------------------------------------

func (b *bot) live() *jsonx.Object {
	b.mu.Lock()
	username := b.username
	running := b.running
	b.mu.Unlock()
	var name any
	if username != "" {
		name = username
	}
	_, claimed, _ := owner.GetOwner(b.c.DB, "telegram")
	return obj("username", name, "ownerClaimed", claimed, "polling", running)
}

func (b *bot) action(ctx context.Context, name string, _ *jsonx.Object) (*jsonx.Object, error) {
	if name != "claimLink" {
		return nil, fmt.Errorf("Unknown Telegram action: %s", name)
	}
	b.mu.Lock()
	username := b.username
	b.mu.Unlock()
	if username == "" {
		me, err := getMe(ctx, b.api)
		if err != nil {
			return nil, err
		}
		b.learnUsername(me.Username)
		username = me.Username
	}
	if username == "" {
		return nil, errors.New("Bot username unknown")
	}
	claim, err := owner.CreateClaim(b.c.DB, "telegram", b.o.Now())
	if err != nil {
		return nil, err
	}
	url := "https://t.me/" + username + "?start=" + claim.Nonce
	svg, err := channelutil.QRSVG(url)
	if err != nil {
		return nil, err
	}
	return obj("url", url, "expiresAt", claim.ExpiresAt, "qrSvg", svg), nil
}
