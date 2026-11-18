package telegram

// A small Bot API client: the slice of grammY the runtime used. Calls go
// through a Transport (method + JSON payload -> result), the way grammY's API
// transformers see them, so tests swap in a fake that records every call
// (tgtest) and the runtime never reaches api.telegram.org in tests.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// APIRoot is grammY's default apiRoot.
const APIRoot = "https://api.telegram.org"

// Transport performs one Bot API call and returns its result. A Telegram
// error (ok: false) is an *APIError; a failed request is an *HTTPError.
type Transport interface {
	Call(ctx context.Context, method string, payload *jsonx.Object) (json.RawMessage, error)
}

// APIError is grammY's GrammyError: Telegram answered with ok: false.
type APIError struct {
	Method      string
	Code        int
	Description string
	// parameters.retry_after, 0 when absent.
	RetryAfter int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Call to '%s' failed! (%d: %s)", e.Method, e.Code, e.Description)
}

// HTTPError is grammY's HttpError: the request itself failed.
type HTTPError struct {
	Method string
	Err    error
}

func (e *HTTPError) Error() string { return fmt.Sprintf("Network request for '%s' failed!", e.Method) }
func (e *HTTPError) Unwrap() error { return e.Err }

// InputFile is a file uploaded with the call: a payload value of this type
// makes the request multipart/form-data, with the file under its key.
type InputFile struct {
	Name     string
	MimeType string
	Bytes    []byte
}

// MarshalJSON keeps the bytes out of logs and recorded calls.
func (f *InputFile) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"file": f.Name, "mimeType": f.MimeType, "size": len(f.Bytes)})
}

func hasFile(payload *jsonx.Object) bool {
	for _, k := range payload.Keys() {
		if v, _ := payload.Get(k); v != nil {
			if _, ok := v.(*InputFile); ok {
				return true
			}
		}
	}
	return false
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

// multipartBody is the payload as form fields: strings as they are, other
// values as JSON (reply_markup, reply_parameters), files as file parts.
func multipartBody(payload *jsonx.Object) (*bytes.Buffer, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, k := range payload.Keys() {
		v, _ := payload.Get(k)
		switch t := v.(type) {
		case *InputFile:
			h := make(textproto.MIMEHeader)
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, quoteEscaper.Replace(k), quoteEscaper.Replace(t.Name)))
			h.Set("Content-Type", t.MimeType)
			part, err := w.CreatePart(h)
			if err != nil {
				return nil, "", err
			}
			if _, err := part.Write(t.Bytes); err != nil {
				return nil, "", err
			}
		case string:
			if err := w.WriteField(k, t); err != nil {
				return nil, "", err
			}
		default:
			raw, err := jsonx.Marshal(v)
			if err != nil {
				return nil, "", err
			}
			if err := w.WriteField(k, string(raw)); err != nil {
				return nil, "", err
			}
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

// HTTPTransport calls the real Bot API with POST <root>/bot<token>/<method>.
type HTTPTransport struct {
	Token  string
	Root   string
	Client *http.Client
}

// NewHTTPTransport is the default transport for a token.
func NewHTTPTransport(token string) Transport {
	return &HTTPTransport{Token: token, Root: APIRoot, Client: &http.Client{}}
}

// requestTimeout is grammY's timeoutSeconds default (500 s).
const requestTimeout = 500 * time.Second

func (h *HTTPTransport) Call(ctx context.Context, method string, payload *jsonx.Object) (json.RawMessage, error) {
	if payload == nil {
		payload = jsonx.NewMap[any]()
	}
	var body io.Reader
	contentType := "application/json"
	if hasFile(payload) {
		buf, ct, err := multipartBody(payload)
		if err != nil {
			return nil, &HTTPError{Method: method, Err: err}
		}
		body, contentType = buf, ct
	} else {
		raw, err := jsonx.Marshal(payload)
		if err != nil {
			return nil, &HTTPError{Method: method, Err: err}
		}
		body = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.Root+"/bot"+h.Token+"/"+method, body)
	if err != nil {
		return nil, &HTTPError{Method: method, Err: err}
	}
	req.Header.Set("content-type", contentType)
	res, err := h.Client.Do(req)
	if err != nil {
		return nil, &HTTPError{Method: method, Err: err}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, &HTTPError{Method: method, Err: err}
	}
	return decodeResponse(method, raw)
}

func decodeResponse(method string, raw []byte) (json.RawMessage, error) {
	var r struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, &HTTPError{Method: method, Err: err}
	}
	if !r.OK {
		return nil, &APIError{Method: method, Code: r.ErrorCode, Description: r.Description, RetryAfter: r.Parameters.RetryAfter}
	}
	return r.Result, nil
}

// --- Bot API types (the fields the runtime reads) ---------------------------------------

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

type Chat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title,omitempty"`
	FirstName string `json:"first_name,omitempty"`
}

type Entity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	User   *User  `json:"user,omitempty"`
}

type PhotoSize struct {
	FileID string `json:"file_id"`
	Width  int64  `json:"width"`
	Height int64  `json:"height"`
}

type Voice struct {
	FileID   string  `json:"file_id"`
	Duration float64 `json:"duration"`
	MimeType *string `json:"mime_type,omitempty"`
}

type Document struct {
	FileID   string   `json:"file_id"`
	FileName *string  `json:"file_name,omitempty"`
	MimeType *string  `json:"mime_type,omitempty"`
	FileSize *float64 `json:"file_size,omitempty"`
}

type Message struct {
	MessageID       int64       `json:"message_id"`
	Date            int64       `json:"date"`
	Chat            Chat        `json:"chat"`
	From            *User       `json:"from,omitempty"`
	Text            *string     `json:"text,omitempty"`
	Caption         *string     `json:"caption,omitempty"`
	Entities        *[]Entity   `json:"entities,omitempty"`
	CaptionEntities *[]Entity   `json:"caption_entities,omitempty"`
	Photo           []PhotoSize `json:"photo,omitempty"`
	Voice           *Voice      `json:"voice,omitempty"`
	Document        *Document   `json:"document,omitempty"`
	ReplyToMessage  *Message    `json:"reply_to_message,omitempty"`
	IsTopicMessage  bool        `json:"is_topic_message,omitempty"`
	MessageThreadID *int64      `json:"message_thread_id,omitempty"`
	DirectTopic     *struct {
		TopicID int64 `json:"topic_id"`
	} `json:"direct_messages_topic,omitempty"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    *string  `json:"data,omitempty"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

type File struct {
	FileID   string `json:"file_id"`
	FilePath string `json:"file_path,omitempty"`
}

// --- calls ----------------------------------------------------------------------------

func obj(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

func call[T any](ctx context.Context, t Transport, method string, payload *jsonx.Object) (T, error) {
	var out T
	raw, err := t.Call(ctx, method, payload)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, &HTTPError{Method: method, Err: err}
	}
	return out, nil
}

func getMe(ctx context.Context, t Transport) (User, error) {
	return call[User](ctx, t, "getMe", jsonx.NewMap[any]())
}
