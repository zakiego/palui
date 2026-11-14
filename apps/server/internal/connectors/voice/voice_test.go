package voice_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/voice"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

type call struct {
	URL    string
	Method string
	Header http.Header
	Body   []byte
}

type mock struct {
	calls   []call
	handler func(url string) (int, any)
}

func (m *mock) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
	}
	m.calls = append(m.calls, call{r.URL.String(), r.Method, r.Header.Clone(), body})
	status, v := m.handler(r.URL.String())
	b, _ := json.Marshal(v)
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(b)), Request: r}, nil
}

func mocked(status int, v any) *mock {
	return &mock{handler: func(string) (int, any) { return status, v }}
}

func (m *mock) client() *http.Client { return &http.Client{Transport: m} }

var audio = connectors.InboundMedia{Kind: "audio", MimeType: "audio/ogg; codecs=opus", Data: base64.StdEncoding.EncodeToString([]byte("fake-ogg-bytes"))}

func config(t *testing.T, raw string) voice.Config {
	o, err := jsonx.ParseObject([]byte(raw))
	testutil.Must(t, err)
	out, err := voice.Manifest.ParseConfig(o)
	testutil.Must(t, err)
	return voice.ConfigFrom(out)
}

func form(t *testing.T, c call) (map[string]string, string, []byte) {
	_, params, err := mime.ParseMediaType(c.Header.Get("content-type"))
	testutil.Must(t, err)
	r := multipart.NewReader(bytes.NewReader(c.Body), params["boundary"])
	fields := map[string]string{}
	var name string
	var file []byte
	for {
		p, err := r.NextPart()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(p)
		if p.FormName() == "file" {
			name, file = p.FileName(), b
		} else {
			fields[p.FormName()] = string(b)
		}
	}
	return fields, name, file
}

func TestManifestDefaultsToScribeV1InIndonesian(t *testing.T) {
	out, err := voice.Manifest.ParseConfig(nil)
	testutil.Must(t, err)
	if got := jsonx.Stringify(out); got != `{"model":"scribe_v1","language":"id"}` {
		t.Fatal(got)
	}
}

func TestTranscribePostsMultipartModelLanguageAndFileWithTheKeyHeader(t *testing.T) {
	m := mocked(200, map[string]any{"language_code": "ind", "text": " halo palui, ingetin aku besok jam 7 "})
	text, err := voice.Transcribe(context.Background(), audio, "xi_key_123456", config(t, `{}`), m.client())
	testutil.Must(t, err)
	if text != "halo palui, ingetin aku besok jam 7" {
		t.Fatal(text)
	}
	c := m.calls[0]
	if c.URL != "https://api.elevenlabs.io/v1/speech-to-text" || c.Method != "POST" || c.Header.Get("xi-api-key") != "xi_key_123456" {
		t.Fatalf("%+v", c)
	}
	fields, name, file := form(t, c)
	if fields["model_id"] != "scribe_v1" || fields["language_code"] != "id" || name != "voice.ogg" || string(file) != "fake-ogg-bytes" {
		t.Fatal(fields, name, string(file))
	}
}

func TestTranscribeOmitsLanguageCodeForAuto(t *testing.T) {
	m := mocked(200, map[string]any{"text": "hello"})
	_, err := voice.Transcribe(context.Background(), audio, "k", config(t, `{"language":"auto"}`), m.client())
	testutil.Must(t, err)
	if fields, _, _ := form(t, m.calls[0]); fields["language_code"] != "" {
		t.Fatal(fields)
	}
}

func TestTranscribeExplainsA401AndOtherErrors(t *testing.T) {
	bad := mocked(401, map[string]any{"detail": map[string]any{"status": "invalid_api_key", "message": "Invalid API key"}})
	if _, err := voice.Transcribe(context.Background(), audio, "k", config(t, `{}`), bad.client()); err == nil || !strings.Contains(err.Error(), "rejected the API key (401)") {
		t.Fatal(err)
	}
	busy := mocked(429, map[string]any{"detail": map[string]any{"status": "too_many_concurrent_requests", "message": "Slow down"}})
	_, err := voice.Transcribe(context.Background(), audio, "k", config(t, `{}`), busy.client())
	var ve *voice.Error
	if !errors.As(err, &ve) || ve.Status != 429 || !strings.Contains(ve.Message, "Slow down") {
		t.Fatal(err)
	}
}

func TestTranscribeRefusesEmptyAudioWithoutCallingOut(t *testing.T) {
	m := mocked(200, map[string]any{})
	empty := audio
	empty.Data = ""
	if _, err := voice.Transcribe(context.Background(), empty, "k", config(t, `{}`), m.client()); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatal(err)
	}
	if len(m.calls) != 0 {
		t.Fatal("called out")
	}
}

func TestCheckKeyPassesWithAWorkingKey(t *testing.T) {
	m := mocked(200, map[string]any{"subscription": map[string]any{"tier": "creator"}})
	res := voice.CheckKey(context.Background(), "k", m.client())
	if !res.OK || res.Detail != "ElevenLabs key works (creator plan)" || m.calls[0].URL != "https://api.elevenlabs.io/v1/user" {
		t.Fatalf("%+v", res)
	}
}

func TestCheckKeyGivesAClearErrorOn401(t *testing.T) {
	res := voice.CheckKey(context.Background(), "k", mocked(401, map[string]any{"detail": map[string]any{"status": "invalid_api_key", "message": "Invalid API key"}}).client())
	if res.OK || !strings.Contains(res.Detail, "rejected the API key (401)") {
		t.Fatalf("%+v", res)
	}
}

func TestCheckKeyAcceptsAKeyScopedWithoutUserRead(t *testing.T) {
	if !voice.CheckKey(context.Background(), "k", mocked(401, map[string]any{"detail": map[string]any{"status": "missing_permissions", "message": "missing user_read"}}).client()).OK {
		t.Fatal("refused")
	}
}
