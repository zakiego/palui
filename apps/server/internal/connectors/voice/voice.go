// Package voice is the voice notes connector (connectors/src/voice):
// ElevenLabs Speech to Text (Scribe). The Gemini fallback isn't built.
package voice

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// ConfigZod is the voice manifest's config.
var ConfigZod = zod.Object(
	zod.F("model", zod.Default(zod.String().Trim().Min(1), "scribe_v1")),
	zod.F("language", zod.Default(zod.String().Trim().Max(10).Optional(), "id")),
)

var SecretZod = []zod.Field{zod.F("elevenLabsKey", zod.String().Min(10))}

var Manifest = connectors.Manifest{
	ID:      "voice",
	Kind:    "skill",
	Name:    "Voice notes",
	Icon:    "mic",
	Summary: "Voice notes, read as text.",
	Setup: []connectors.SetupStep{
		{Field: "elevenLabsKey", Label: "ElevenLabs API key", Secret: true, Help: "ElevenLabs > API keys, with Speech to Text access."},
		{Field: "language", Label: "Language", Optional: true, Placeholder: "id", Help: "id for Indonesian, auto to detect."},
	},
	PauseNote:    "While paused, voice notes are not transcribed.",
	Docs:         &connectors.Docs{Notes: []string{"Fallback engine: none yet. CLIProxyAPI has no Gemini models, so Gemini audio isn't wired."}},
	SecretFields: kit.SecretNames(SecretZod),
	ConfigZod:    ConfigZod,
	SecretZod:    SecretZod,
	ParseConfig:  kit.ParseConfig(ConfigZod),
	ParseSecrets: kit.ParseSecrets(SecretZod),
}

const (
	ElevenLabsAPI = "https://api.elevenlabs.io"
	MaxAudioBytes = 25 * 1024 * 1024
)

// Error is VoiceError.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string  { return e.Message }
func (e *Error) JSName() string { return "VoiceError" }

// HTTPStatus is the provider's status (0 when the request never got one), so
// callers can tell a rate limit or outage from a bad key.
func (e *Error) HTTPStatus() int { return e.Status }

var ext = map[string]string{
	"audio/ogg": "ogg", "audio/opus": "opus", "audio/mpeg": "mp3", "audio/mp4": "m4a", "audio/x-m4a": "m4a",
	"audio/aac": "aac", "audio/wav": "wav", "audio/x-wav": "wav", "audio/webm": "webm",
}

func fileName(a connectors.InboundMedia) string {
	if a.Filename != "" {
		return a.Filename
	}
	base := strings.ToLower(strx.Trim(strings.Split(a.MimeType, ";")[0]))
	if e, ok := ext[base]; ok {
		return "voice." + e
	}
	return "voice.ogg"
}

func errorDetail(res *http.Response) (status, message string) {
	raw, _ := io.ReadAll(res.Body)
	text := string(raw)
	var body struct {
		Detail json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		m := strx.Slice16(text, 200)
		if m == "" {
			m = http.StatusText(res.StatusCode)
		}
		return "", m
	}
	var s string
	if json.Unmarshal(body.Detail, &s) == nil {
		return "", s
	}
	var d struct {
		Status  string  `json:"status"`
		Message *string `json:"message"`
	}
	_ = json.Unmarshal(body.Detail, &d)
	if d.Message != nil {
		return d.Status, *d.Message
	}
	return d.Status, strx.Slice16(text, 200)
}

// Config is the voice config the runtime reads.
type Config struct {
	Model    string
	Language string
}

// ConfigFrom reads ctx.config.
func ConfigFrom(o *jsonx.Object) Config {
	s := func(k string) string {
		if o == nil {
			return ""
		}
		v, _ := o.Get(k)
		x, _ := v.(string)
		return x
	}
	return Config{Model: s("model"), Language: s("language")}
}

func client(hc *http.Client) *http.Client {
	if hc == nil {
		return http.DefaultClient
	}
	return hc
}

// Transcribe is transcribeWithElevenLabs.
func Transcribe(ctx context.Context, audio connectors.InboundMedia, apiKey string, cfg Config, hc *http.Client) (string, error) {
	data, _ := base64.StdEncoding.DecodeString(audio.Data)
	if len(data) == 0 {
		data, _ = base64.RawStdEncoding.DecodeString(strings.TrimRight(audio.Data, "="))
	}
	if len(data) == 0 {
		return "", &Error{400, "The voice note is empty"}
	}
	if len(data) > MaxAudioBytes {
		return "", &Error{413, "The voice note is larger than 25 MB"}
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	model := cfg.Model
	if model == "" {
		model = "scribe_v1"
	}
	_ = w.WriteField("model_id", model)
	if lang := strx.Trim(cfg.Language); lang != "" && lang != "auto" {
		_ = w.WriteField("language_code", lang)
	}
	_ = w.WriteField("tag_audio_events", "false")
	mt := audio.MimeType
	if mt == "" {
		mt = "application/octet-stream"
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, fileName(audio)))
	h.Set("Content-Type", mt)
	part, _ := w.CreatePart(h)
	_, _ = part.Write(data)
	_ = w.Close()
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", ElevenLabsAPI+"/v1/speech-to-text", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("xi-api-key", apiKey)
	req.Header.Set("content-type", w.FormDataContentType())
	res, err := client(hc).Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		_, msg := errorDetail(res)
		if res.StatusCode == 401 {
			return "", &Error{401, "ElevenLabs rejected the API key (401): " + msg}
		}
		return "", &Error{res.StatusCode, fmt.Sprintf("ElevenLabs answered %d: %s", res.StatusCode, msg)}
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", err
	}
	return strx.Trim(out.Text), nil
}

// CheckKey is checkElevenLabsKey: a cheap authenticated call.
func CheckKey(ctx context.Context, apiKey string, hc *http.Client) connectors.TestResult {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", ElevenLabsAPI+"/v1/user", nil)
	if err != nil {
		return connectors.TestResult{OK: false, Detail: "Can't reach ElevenLabs: " + err.Error()}
	}
	req.Header.Set("xi-api-key", apiKey)
	res, err := client(hc).Do(req)
	if err != nil {
		return connectors.TestResult{OK: false, Detail: "Can't reach ElevenLabs: " + err.Error()}
	}
	defer res.Body.Close()
	if res.StatusCode >= 200 && res.StatusCode <= 299 {
		var user struct {
			Subscription *struct {
				Tier string `json:"tier"`
			} `json:"subscription"`
		}
		_ = json.NewDecoder(res.Body).Decode(&user)
		if user.Subscription != nil && user.Subscription.Tier != "" {
			return connectors.TestResult{OK: true, Detail: fmt.Sprintf("ElevenLabs key works (%s plan)", user.Subscription.Tier)}
		}
		return connectors.TestResult{OK: true, Detail: "ElevenLabs key works"}
	}
	status, msg := errorDetail(res)
	if res.StatusCode == 401 && status == "missing_permissions" {
		return connectors.TestResult{OK: true, Detail: "ElevenLabs key works (it can't read account info, which is fine)"}
	}
	if res.StatusCode == 401 {
		return connectors.TestResult{OK: false, Detail: "ElevenLabs rejected the API key (401). Check it in elevenlabs.io, Developers, API keys."}
	}
	return connectors.TestResult{OK: false, Detail: fmt.Sprintf("ElevenLabs answered %d: %s", res.StatusCode, msg)}
}

// New is the runtime with an injectable HTTP client.
func New(hc *http.Client) *connectors.Runtime {
	return &connectors.Runtime{
		Manifest: Manifest,
		Test: func(c connectors.Context) (connectors.TestResult, error) {
			key := c.Secret("elevenLabsKey")
			if key == "" {
				return connectors.TestResult{OK: false, Detail: "Add the ElevenLabs API key"}, nil
			}
			ctx := c.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			return CheckKey(ctx, key, hc), nil
		},
		Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
			key := c.Secret("elevenLabsKey")
			if key == "" {
				return nil, errors.New("Add the ElevenLabs API key")
			}
			cfg := ConfigFrom(c.Config)
			var mu sync.Mutex
			transcribed, failed := 0, 0
			return &connectors.RunningConnector{
				Stop: func(context.Context) error { return nil },
				Transcribe: func(ctx context.Context, audio connectors.InboundMedia) (string, error) {
					text, err := Transcribe(ctx, audio, key, cfg, hc)
					mu.Lock()
					if err != nil {
						failed++
					} else {
						transcribed++
					}
					mu.Unlock()
					return text, err
				},
				Live: func() *jsonx.Object {
					mu.Lock()
					defer mu.Unlock()
					lang := cfg.Language
					if lang == "" {
						lang = "auto"
					}
					return kit.O("engine", "elevenlabs", "model", cfg.Model, "language", lang, "transcribed", transcribed, "failed", failed, "fallback", nil)
				},
			}, nil
		},
	}
}

// Runtime is the voice connector runtime.
var Runtime = New(nil)
