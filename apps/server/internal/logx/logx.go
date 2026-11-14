// Package logx ports packages/core/src/log.ts: a JSON-lines logger with secret
// redaction. Every line can carry run_id, chat_id and connector_id through
// child loggers.
package logx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`\brcl_[A-Za-z0-9_-]{6,}`),
	regexp.MustCompile(`\bzkg_[A-Za-z0-9_-]{6,}`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}`),
	nil, // Telegram bot token, see redactBotTokens.
	regexp.MustCompile(`(?i)\bBearer[` + strx.SpaceClass + `]+[A-Za-z0-9._~+/=-]{12,}`),
}

// The TS pattern is /(?<!\d)\d{6,12}:[A-Za-z0-9_-]{30,}/g. RE2 has no
// lookbehind, so the "no digit before" rule is applied by hand, retrying one
// position later like a JS regex scan does.
var botToken = regexp.MustCompile(`\d{6,12}:[A-Za-z0-9_-]{30,}`)

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func redactBotTokens(s string) string {
	var out bytes.Buffer
	pos := 0
	last := 0
	for pos <= len(s) {
		loc := botToken.FindStringIndex(s[pos:])
		if loc == nil {
			break
		}
		start, end := pos+loc[0], pos+loc[1]
		if start > 0 && isDigit(s[start-1]) {
			// JS tries the next start position; the digit run before it keeps
			// failing the lookbehind until the run ends, so skip ahead one.
			pos = start + 1
			continue
		}
		out.WriteString(s[last:start])
		out.WriteString(mask(s[start:end]))
		last = end
		pos = end
	}
	if last == 0 {
		return s
	}
	out.WriteString(s[last:])
	return out.String()
}

func mask(m string) string { return strx.Slice16(m, 4) + "••••" }

// Redact hides known secret shapes in s.
func Redact(s string) string {
	out := s
	for _, p := range patterns {
		if p == nil {
			out = redactBotTokens(out)
			continue
		}
		out = p.ReplaceAllStringFunc(out, mask)
	}
	return out
}

// RedactDeep applies Redact to every string inside a JSON-like value.
func RedactDeep(v any) any {
	switch t := v.(type) {
	case string:
		return Redact(t)
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = RedactDeep(x)
		}
		return out
	case []string:
		out := make([]string, len(t))
		for i, x := range t {
			out[i] = Redact(x)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = RedactDeep(x)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(t))
		for k, x := range t {
			out[k] = Redact(x)
		}
		return out
	case *jsonx.Object:
		if t == nil {
			return t
		}
		out := jsonx.NewMap[any]()
		for _, k := range t.Keys() {
			x, _ := t.Get(k)
			out.Set(k, RedactDeep(x))
		}
		return out
	}
	return v
}

type Level string

const (
	Debug Level = "debug"
	Info  Level = "info"
	Warn  Level = "warn"
	Error Level = "error"
)

var order = map[Level]int{Debug: 10, Info: 20, Warn: 30, Error: 40}

// Fields are extra keys on a line. Keys are written sorted.
type Fields map[string]any

// Sink receives each finished line (no newline).
type Sink func(line string)

// Logger writes JSON lines: {t, level, msg, ...base, ...fields}.
type Logger struct {
	base []kv
	sink Sink
	min  Level
}

type kv struct {
	k string
	v any
}

// StdoutSink writes lines to stdout.
func StdoutSink() Sink { return WriterSink(os.Stdout) }

// WriterSink writes each line plus "\n" to w.
func WriterSink(w io.Writer) Sink {
	var mu sync.Mutex
	return func(line string) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintln(w, line)
	}
}

// New makes a logger. An empty min reads LOG_LEVEL, then "info".
func New(base Fields, sink Sink, min Level) *Logger {
	if sink == nil {
		sink = StdoutSink()
	}
	if min == "" {
		min = Level(os.Getenv("LOG_LEVEL"))
	}
	if _, ok := order[min]; !ok {
		min = Info
	}
	return &Logger{base: sortedKV(nil, base), sink: sink, min: min}
}

func sortedKV(prev []kv, f Fields) []kv {
	out := append([]kv(nil), prev...)
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		replaced := false
		for i := range out {
			if out[i].k == k {
				out[i].v = f[k]
				replaced = true
			}
		}
		if !replaced {
			out = append(out, kv{k, f[k]})
		}
	}
	return out
}

// Silent drops everything.
var Silent = &Logger{sink: func(string) {}, min: Error + "x"}

func errorValue(v any) any {
	if err, ok := v.(error); ok {
		name := "Error"
		var named interface{ Name() string }
		if errors.As(err, &named) {
			name = named.Name()
		}
		obj := jsonx.NewMap[any]()
		obj.Set("name", name)
		obj.Set("message", err.Error())
		return obj
	}
	return v
}

func (l *Logger) write(level Level, msg string, fields Fields) {
	if l == nil || l.sink == nil {
		return
	}
	minOrder, ok := order[l.min]
	if !ok || order[level] < minOrder {
		return
	}
	t := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	rec := jsonx.NewMap[any]()
	rec.Set("t", t)
	rec.Set("level", string(level))
	rec.Set("msg", msg)
	for _, p := range sortedKV(l.base, fields) {
		rec.Set(p.k, errorValue(p.v))
	}
	b, err := jsonx.Marshal(rec)
	if err != nil {
		note := jsonx.NewMap[any]()
		note.Set("t", t)
		note.Set("level", string(level))
		note.Set("msg", msg)
		note.Set("note", "unserializable fields")
		b, _ = jsonx.Marshal(note)
	}
	l.sink(Redact(string(b)))
}

func (l *Logger) Debug(msg string, f ...Fields) { l.write(Debug, msg, merge(f)) }
func (l *Logger) Info(msg string, f ...Fields)  { l.write(Info, msg, merge(f)) }
func (l *Logger) Warn(msg string, f ...Fields)  { l.write(Warn, msg, merge(f)) }
func (l *Logger) Error(msg string, f ...Fields) { l.write(Error, msg, merge(f)) }

// Child adds fields to every line.
func (l *Logger) Child(f Fields) *Logger {
	if l == nil {
		return nil
	}
	return &Logger{base: sortedKV(l.base, f), sink: l.sink, min: l.min}
}

func merge(fs []Fields) Fields {
	if len(fs) == 0 {
		return nil
	}
	if len(fs) == 1 {
		return fs[0]
	}
	out := Fields{}
	for _, f := range fs {
		for k, v := range f {
			out[k] = v
		}
	}
	return out
}
