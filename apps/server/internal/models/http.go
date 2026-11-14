package models

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// UserAgent is sent on every model request.
const UserAgent = "palui"

// postJSON sends the body and returns the response when the status is 2xx.
// A failed status becomes an APICallError with the provider's message
// (errMessage reads it from the JSON body; a body that isn't that shape uses
// the status text).
func postJSON(ctx context.Context, client *http.Client, url string, headers map[string]string, body any, errMessage func([]byte) (string, bool)) (*http.Response, error) {
	b, err := jsonx.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", UserAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &APICallError{Message: "Cannot connect to API: " + err.Error(), URL: url, IsRetryable: true, Cause: err}
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return res, nil
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	msg, ok := errMessage(raw)
	if !ok {
		msg = statusText(res)
	}
	s := res.StatusCode
	return nil, &APICallError{Message: msg, URL: url, StatusCode: s, ResponseBody: string(raw), IsRetryable: s == 408 || s == 409 || s == 429 || s >= 500}
}

// statusText is the fetch Response's statusText: the reason phrase the server
// sent, which Go only exposes inside Status.
func statusText(res *http.Response) string {
	_, text, _ := strings.Cut(res.Status, " ")
	return text
}

// sseReader reads Server-Sent Events: each event's data lines joined.
type sseReader struct {
	r    *bufio.Reader
	body io.Closer
}

func newSSE(body io.ReadCloser) *sseReader {
	return &sseReader{r: bufio.NewReaderSize(body, 64*1024), body: body}
}

// next returns the next event's data (event name ignored), io.EOF at the end.
func (s *sseReader) next() (string, error) {
	var data []string
	has := false
	for {
		line, err := s.r.ReadString('\n')
		if err != nil && line == "" {
			if has {
				return strings.Join(data, "\n"), nil
			}
			return "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if has {
				return strings.Join(data, "\n"), nil
			}
			if err != nil {
				return "", err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		if field == "data" {
			data = append(data, value)
			has = true
		}
		if err != nil {
			return strings.Join(data, "\n"), nil
		}
	}
}

func (s *sseReader) close() error { return s.body.Close() }

// eventStream turns a producer into a Stream: produce is called until it
// returns no more events; its events are queued.
type eventStream struct {
	queue   []Event
	produce func() ([]Event, bool)
	done    bool
	closer  func() error
	closed  bool
}

func (s *eventStream) Recv() (Event, error) {
	for len(s.queue) == 0 {
		if s.done {
			return Event{}, io.EOF
		}
		evs, more := s.produce()
		s.queue = append(s.queue, evs...)
		if !more {
			s.done = true
		}
	}
	ev := s.queue[0]
	s.queue = s.queue[1:]
	return ev, nil
}

func (s *eventStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.closer()
}

// readErrorMessage reads {error: {message}} bodies.
func readErrorMessage(raw []byte) (string, bool) {
	var v struct {
		Error *struct {
			Message *string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Error == nil || v.Error.Message == nil {
		return "", false
	}
	return *v.Error.Message, true
}

// isCanceled reports a read error caused by the caller's context.
func isCanceled(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errors.Is(err, context.Canceled)
}
