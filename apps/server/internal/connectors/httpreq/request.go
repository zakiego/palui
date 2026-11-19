package httpreq

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/safenet"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	MaxBodyChars    = 16_000  // UTF-16 units shown to the model
	maxReadBytes    = 1 << 20 // decoded bytes read from the response
	maxRequestBytes = 256 * 1024
	maxHops         = 5
	defaultTimeout  = 20 // seconds, across every hop
	maxTimeout      = 60
)

// Options are what tests inject.
type Options struct {
	Resolver  safenet.Resolver
	Transport safenet.Transport
}

type Request struct {
	Method     string
	URL        string
	Headers    map[string]string
	Body       *string
	TimeoutSec int64
}

var redirectStatus = map[int]bool{301: true, 302: true, 303: true, 307: true, 308: true}

// Response headers worth showing the model.
var shownHeaders = map[string]bool{
	"content-type": true, "content-length": true, "location": true, "retry-after": true, "link": true,
	"etag": true, "last-modified": true, "www-authenticate": true, "x-request-id": true,
}

var shownHeaderPrefixes = []string{"x-ratelimit-", "ratelimit-", "x-rate-limit-"}

const (
	maxHeaderChars       = 500 // UTF-16 units of one shown header value
	maxPrefixedHeaders   = 10  // shown headers matched by a prefix
	maxPrefixedNameChars = 64  // longer prefix-matched names are left out
	maxContentTypeChars  = 100
	maxURLChars          = 2000 // the url field of a result
	maxErrorChars        = 500
	maxRedirectURLLen    = 4000 // the same limit as the url input
)

func isWrite(method string) bool { return method != "GET" && method != "HEAD" }

// Do sends one request under the instance's rules and returns the result object.
func Do(ctx context.Context, cfg Config, secret string, req Request, o Options) *jsonx.Object {
	if ctx == nil {
		ctx = context.Background()
	}
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = "GET"
	}
	current := strx.Trim(req.URL)
	fail := func(href, msg string) *jsonx.Object {
		return kit.O("ok", false, "url", clip(Redact(href, secret), maxURLChars), "error", clip(Redact(msg, secret), maxErrorChars))
	}
	if err := CheckHeaders(cfg, req.Headers); err != nil {
		return fail(current, err.Error())
	}
	if req.Body != nil && len(*req.Body) > maxRequestBytes {
		return fail(current, "The request body is over 256 KiB.")
	}
	timeout := req.TimeoutSec
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	timeout = min(timeout, maxTimeout)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()

	u, err := safenet.AssertPublicURL(current)
	if err != nil {
		return fail(current, friendlyError(err))
	}
	if msg := refuse(cfg, u); msg != "" {
		return fail(current, msg)
	}
	for hop := 0; ; hop++ {
		href, headers := Attach(cfg, secret, u, req.Headers)
		left := time.Until(deadline).Milliseconds()
		if left <= 0 {
			return fail(u.Href(), "The API took too long to answer")
		}
		res, err := safenet.Fetch(ctx, href, safenet.Options{
			Method: method, Headers: headers, Body: req.Body, TimeoutMs: left,
			MaxBytes: maxReadBytes, Truncate: true, ManualRedirects: true,
			Resolver: o.Resolver, Transport: o.Transport,
		})
		if err != nil {
			return fail(u.Href(), friendlyError(err))
		}
		location := res.Header.Get("location")
		if !redirectStatus[res.Status] || location == "" {
			return result(res, method, secret, "")
		}
		if isWrite(method) {
			return result(res, method, secret, "Redirects aren't followed for writes.")
		}
		stop := func(msg string) *jsonx.Object {
			return kit.O("ok", false, "status", res.Status, "url", clip(Redact(res.URL, secret), maxURLChars), "error", clip(Redact(msg, secret), maxErrorChars))
		}
		if hop+1 > maxHops {
			return stop(fmt.Sprintf("More than %d redirects", maxHops))
		}
		next, err := u.Resolve(location)
		if err != nil {
			return stop("The API redirected to an invalid URL. Not followed.")
		}
		if len(next.Href()) > maxRedirectURLLen {
			return stop(fmt.Sprintf("The API redirected to a URL over %d characters. Not followed.", maxRedirectURLLen))
		}
		if _, err := safenet.AssertPublicURL(next.Href()); err != nil {
			return stop(friendlyError(err))
		}
		if !Allowed(cfg.Domains, next.HostOf()) {
			return stop("Redirected to " + next.HostOf() + ", which isn't in the allowed domains. Not followed.")
		}
		if cfg.Auth != "none" && next.Scheme != "https" {
			return stop("Redirected to " + next.HostOf() + " over " + next.Scheme + ". The key is only sent over https. Not followed.")
		}
		if cfg.Auth != "none" && next.Port != "" {
			return stop("Redirected to " + next.HostOf() + " on port " + next.Port + ". The key is only sent to port 443. Not followed.")
		}
		u = next
	}
}

// refuse is why a URL can't be called with this instance, "" when it can.
func refuse(cfg Config, u *safenet.URL) string {
	if !Allowed(cfg.Domains, u.HostOf()) {
		return u.HostOf() + " isn't in this connector's allowed domains (" + joinDomains(cfg.Domains) + ")."
	}
	if cfg.Auth != "none" && u.Scheme != "https" {
		return "The key is only sent over https."
	}
	if cfg.Auth != "none" && u.Port != "" {
		return "The key is only sent to port 443."
	}
	return ""
}

func textual(typ string, b []byte) bool {
	switch {
	case typ == "":
		return utf8.Valid(b)
	case strings.HasPrefix(typ, "text/"), typ == "application/json", strings.HasSuffix(typ, "+json"),
		typ == "application/xml", strings.HasSuffix(typ, "+xml"), typ == "application/x-www-form-urlencoded":
		return true
	}
	return false
}

func result(res *safenet.Response, method, secret, note string) *jsonx.Object {
	headers := jsonx.NewMap[any]()
	var names []string
	for k := range res.Header {
		names = append(names, strings.ToLower(k))
	}
	sort.Strings(names)
	prefixed := 0
	for _, name := range names {
		if !showHeader(name) {
			continue
		}
		if !shownHeaders[name] {
			// Prefix-matched names come from the server, so a hostile one can be
			// huge. Real rate-limit header names are well under 64 characters.
			if prefixed == maxPrefixedHeaders || len(name) > maxPrefixedNameChars {
				continue
			}
			prefixed++
		}
		headers.Set(name, clip(Redact(strings.Join(res.Header.Values(name), ", "), secret), maxHeaderChars))
	}
	contentType := clip(Redact(res.ContentType, secret), maxContentTypeChars)
	body, truncated := "", res.Truncated
	switch {
	case method == "HEAD":
	case textual(res.ContentType, res.Bytes):
		body = Redact(res.Text(), secret)
		if strx.Len16(body) > MaxBodyChars {
			body = strx.Slice16(body, MaxBodyChars) + "\n[truncated]"
			truncated = true
		}
	default:
		if len(res.Bytes) > 0 {
			typ := contentType
			if typ == "" {
				typ = "unknown type"
			}
			if note != "" {
				note += " "
			}
			note += fmt.Sprintf("Binary response (%s, %d bytes) isn't shown.", typ, len(res.Bytes))
		}
	}
	return kit.O("ok", res.OK, "status", res.Status, "url", clip(Redact(res.URL, secret), maxURLChars), "headers", headers,
		"contentType", contentType, "body", body, "truncated", truncated, "note", kit.If(note != "", note))
}

func showHeader(name string) bool {
	if shownHeaders[name] {
		return true
	}
	for _, p := range shownHeaderPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// friendlyError is webfetch's wording for safenet errors.
func friendlyError(err error) string {
	var se *safenet.Error
	if errors.As(err, &se) {
		switch se.Code {
		case "blocked_address", "blocked_host", "blocked_port", "blocked_protocol", "blocked_credentials":
			return "Refused by the SSRF guard: " + se.Message
		case "timeout":
			return "The API took too long to answer"
		}
		return se.Message
	}
	return err.Error()
}
