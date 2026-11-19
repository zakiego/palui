package safenet

import (
	"bufio"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/unicode"

	"github.com/zakiego/palui/apps/server/internal/strx"
)

// SafeFetch is the only way agent-steered code fetches a URL. On top of the
// static checks in AssertPublicURL it:
//   - resolves DNS itself and refuses a host if any address is internal,
//   - pins the connection to the address it checked, so a second DNS answer
//     can't rebind the socket to 127.0.0.1,
//   - follows redirects by hand (max 5) and re-runs every check on each hop,
//   - caps the decoded body size (gzip bombs count after decompression),
//   - enforces an overall timeout and an optional content type allowlist.

// --- Transport -------------------------------------------------------------------------

type TransportRequest struct {
	URL *URL
	// The address the guard approved. The transport must connect to this, never re-resolve.
	Address ResolvedAddress
	Method  string
	Headers map[string]string
	Body    *string
	Ctx     context.Context
}

type TransportResponse struct {
	Status int
	Header http.Header
	// Decoded (decompressed) body.
	Body io.Reader
	// Drop the body without reading it (redirects, refused content types).
	Cancel func()
}

type Transport func(req TransportRequest) (*TransportResponse, error)

// lazyReader builds its reader on the first Read, so a bad gzip header is a
// read error (as in Node) rather than a response error.
type lazyReader struct {
	make func() (io.Reader, error)
	r    io.Reader
	err  error
}

func (l *lazyReader) Read(p []byte) (int, error) {
	if l.r == nil && l.err == nil {
		l.r, l.err = l.make()
	}
	if l.err != nil {
		return 0, l.err
	}
	return l.r.Read(p)
}

func decoder(encoding string, body io.Reader) io.Reader {
	switch strings.ToLower(strx.Trim(encoding)) {
	case "gzip", "x-gzip":
		return &lazyReader{make: func() (io.Reader, error) { return gzip.NewReader(body) }}
	case "deflate":
		return &lazyReader{make: func() (io.Reader, error) { return zlib.NewReader(body) }}
	case "br":
		return brotli.NewReader(body)
	}
	return body
}

// HTTPTransport is net/http with the socket pinned to the approved address.
// TLS still verifies the certificate against the hostname (SNI), so pinning
// doesn't weaken HTTPS.
func HTTPTransport(req TransportRequest) (*TransportResponse, error) {
	host := req.URL.HostOf()
	port := strconv.Itoa(req.URL.EffectivePort())
	target := net.JoinHostPort(req.Address.Address, port)
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, target)
		},
		TLSClientConfig:    &tls.Config{ServerName: host},
		DisableCompression: true,
		DisableKeepAlives:  true,
	}
	var body io.Reader
	if req.Body != nil {
		body = strings.NewReader(*req.Body)
	}
	href := req.URL.Scheme + "://" + req.URL.HostPort() + req.URL.RequestURI()
	hreq, err := http.NewRequestWithContext(req.Ctx, req.Method, href, body)
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("accept-encoding", "gzip, deflate, br")
	for k, v := range req.Headers {
		hreq.Header.Set(k, v)
	}
	res, err := tr.RoundTrip(hreq)
	if err != nil {
		tr.CloseIdleConnections()
		return nil, err
	}
	cancel := func() {
		res.Body.Close()
		tr.CloseIdleConnections()
	}
	return &TransportResponse{
		Status: res.StatusCode,
		Header: res.Header,
		Body:   decoder(res.Header.Get("content-encoding"), res.Body),
		Cancel: cancel,
	}, nil
}

// --- SafeFetch -------------------------------------------------------------------------

type Options struct {
	// "GET" (default), or any other HTTP method.
	Method  string
	Headers map[string]string
	Body    *string
	// Overall budget across every redirect hop. Default 15 s.
	TimeoutMs int64
	// Decoded body cap. Default 5 MB.
	MaxBytes int64
	// True: stop reading at MaxBytes and mark the response truncated. False:
	// fail with too_large.
	Truncate bool
	// Default 5.
	MaxRedirects *int
	// True: return a redirect answer (3xx with a Location) as the response instead of following it.
	ManualRedirects bool
	// Media types allowed on a 2xx answer: "text/html", "text/*",
	// "application/json", "+json". Nil allows everything.
	AllowedContentTypes []string
	Resolver            Resolver
	Transport           Transport
}

type Response struct {
	// Final URL after redirects.
	URL    string
	Status int
	OK     bool
	Header http.Header
	// Media type without parameters, lowercased.
	ContentType string
	// Lowercased, "" when none.
	Charset   string
	Bytes     []byte
	Truncated bool
	// Every URL visited before the final one.
	Redirects []string
}

// Text decodes the body with its charset (UTF-8 when unknown).
func (r *Response) Text() string { return decodeText(r.Bytes, r.Charset) }

// JSON parses the body.
func (r *Response) JSON(v any) error { return json.Unmarshal([]byte(r.Text()), v) }

const DefaultUserAgent = "PaluiAgent/1.0 (+https://palui.zakiego.com)"

var redirects = map[int]bool{301: true, 302: true, 303: true, 307: true, 308: true}

var charsetRe = regexp.MustCompile(`(?i)^\s*charset\s*=\s*"?([^";\s]+)"?`)

// ParseContentType splits a Content-Type header into the media type and charset.
func ParseContentType(header string) (typ string, charset string) {
	if header == "" {
		return "", ""
	}
	parts := strings.Split(header, ";")
	for _, p := range parts[1:] {
		if m := charsetRe.FindStringSubmatch(p); m != nil {
			charset = strings.ToLower(m[1])
		}
	}
	return strings.ToLower(strx.Trim(parts[0])), charset
}

// ContentTypeAllowed checks a media type against rules like "text/*" and "+json".
func ContentTypeAllowed(typ string, allowed []string) bool {
	for _, a := range allowed {
		rule := strings.ToLower(a)
		switch {
		case strings.HasPrefix(rule, "+"):
			if strings.HasSuffix(typ, rule) {
				return true
			}
		case strings.HasSuffix(rule, "/*"):
			if strings.HasPrefix(typ, rule[:len(rule)-1]) {
				return true
			}
		case typ == rule:
			return true
		}
	}
	return false
}

func decodeText(b []byte, charset string) string {
	if charset == "" {
		return strx.TextDecode(b)
	}
	enc, err := htmlindex.Get(charset)
	if err != nil {
		return strx.TextDecode(b)
	}
	if name, _ := htmlindex.Name(enc); name == "utf-8" {
		return strx.TextDecode(b)
	}
	if name, _ := htmlindex.Name(enc); name == "utf-16le" || name == "utf-16be" {
		enc = unicode.UTF16(map[bool]unicode.Endianness{true: unicode.LittleEndian, false: unicode.BigEndian}[name == "utf-16le"], unicode.UseBOM)
	}
	out, err := enc.NewDecoder().Bytes(b)
	if err != nil {
		return strx.TextDecode(b)
	}
	return string(out)
}

func readBody(res *TransportResponse, maxBytes int64, truncate bool) ([]byte, bool, error) {
	r := bufio.NewReader(res.Body)
	b, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		res.Cancel()
		return nil, false, err
	}
	if int64(len(b)) > maxBytes {
		res.Cancel()
		if !truncate {
			return nil, false, errf(CodeTooLarge, "Response is larger than %d bytes", maxBytes)
		}
		return b[:maxBytes], true, nil
	}
	return b, false, nil
}

var errTimeout = errors.New("safenet: timeout")

// Fetch is safeFetch: it returns non-2xx answers instead of failing.
func Fetch(parent context.Context, input string, opts Options) (*Response, error) {
	if parent == nil {
		parent = context.Background()
	}
	resolver := opts.Resolver
	if resolver == nil {
		resolver = SystemResolver
	}
	transport := opts.Transport
	if transport == nil {
		transport = HTTPTransport
	}
	maxBytes := opts.MaxBytes
	if maxBytes == 0 {
		maxBytes = 5 * 1024 * 1024
	}
	maxRedirects := 5
	if opts.MaxRedirects != nil {
		maxRedirects = *opts.MaxRedirects
	}
	timeoutMs := opts.TimeoutMs
	if timeoutMs == 0 {
		timeoutMs = 15_000
	}
	ctx, cancel := context.WithTimeoutCause(parent, time.Duration(timeoutMs)*time.Millisecond, errTimeout)
	defer cancel()

	method := opts.Method
	if method == "" {
		method = "GET"
	}
	body := opts.Body
	headers := map[string]string{"user-agent": DefaultUserAgent, "accept": "*/*"}
	for k, v := range opts.Headers {
		headers[strings.ToLower(k)] = v
	}
	var visited []string
	current := input

	fail := func(err error) error {
		var e *Error
		if errors.As(err, &e) {
			return e
		}
		if errors.Is(context.Cause(ctx), errTimeout) {
			return errf(CodeTimeout, "Timed out after %d ms", timeoutMs)
		}
		if ctx.Err() != nil {
			return errf(CodeAborted, "Request was aborted")
		}
		return &Error{Code: CodeNetwork, Message: err.Error()}
	}

	for hop := 0; ; hop++ {
		u, err := AssertPublicURL(current)
		if err != nil {
			return nil, fail(err)
		}
		address, err := ResolvePublic(ctx, u.HostOf(), resolver)
		if err != nil {
			return nil, fail(err)
		}
		if ctx.Err() != nil {
			return nil, fail(ctx.Err())
		}
		hdrs := make(map[string]string, len(headers))
		for k, v := range headers {
			hdrs[k] = v
		}
		res, err := transport(TransportRequest{URL: u, Address: address, Method: method, Headers: hdrs, Body: body, Ctx: ctx})
		if err != nil {
			return nil, fail(err)
		}

		location := res.Header.Get("location")
		if redirects[res.Status] && location != "" && !opts.ManualRedirects {
			res.Cancel()
			if hop >= maxRedirects {
				return nil, errf(CodeTooManyRedirects, "More than %d redirects", maxRedirects)
			}
			next, err := u.Resolve(location)
			if err != nil {
				return nil, &Error{Code: CodeNetwork, Message: "Invalid URL"}
			}
			visited = append(visited, u.Href())
			// 303, and 301/302 after a POST, switch to GET without a body.
			if res.Status == 303 || ((res.Status == 301 || res.Status == 302) && method == "POST") {
				method = "GET"
				body = nil
				delete(headers, "content-type")
				delete(headers, "content-length")
			}
			// Never carry credentials to another origin.
			if next.Origin() != u.Origin() {
				delete(headers, "authorization")
				delete(headers, "cookie")
			}
			current = next.Href()
			continue
		}

		typ, charset := ParseContentType(res.Header.Get("content-type"))
		ok := res.Status >= 200 && res.Status < 300
		if ok && opts.AllowedContentTypes != nil && !ContentTypeAllowed(typ, opts.AllowedContentTypes) {
			res.Cancel()
			shown := typ
			if shown == "" {
				shown = "(none)"
			}
			return nil, errf(CodeContentType, "Content type %s is not allowed", shown)
		}
		if !opts.Truncate && res.Header.Get("content-encoding") == "" {
			if declared, err := strconv.ParseFloat(strx.Trim(res.Header.Get("content-length")), 64); err == nil && declared > float64(maxBytes) {
				res.Cancel()
				return nil, errf(CodeTooLarge, "Response is larger than %d bytes", maxBytes)
			}
		}
		var data []byte
		truncated := false
		if method == "HEAD" {
			res.Cancel()
			data = []byte{}
		} else {
			data, truncated, err = readBody(res, maxBytes, opts.Truncate)
			if err != nil {
				return nil, fail(err)
			}
			res.Cancel()
		}
		if visited == nil {
			visited = []string{}
		}
		return &Response{
			URL: u.Href(), Status: res.Status, OK: ok, Header: res.Header, ContentType: typ, Charset: charset,
			Bytes: data, Truncated: truncated, Redirects: visited,
		}, nil
	}
}

// String is for logs.
func (r *Response) String() string { return fmt.Sprintf("%d %s", r.Status, r.URL) }
