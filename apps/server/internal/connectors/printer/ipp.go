package printer

// A small IPP/1.1 client (RFC 8010 encoding, RFC 8011 operations): just what
// printing a file and reading a queue's state need. IPP is a binary message
// POSTed over HTTP as application/ipp, with the document bytes after it.

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Operation ids.
const (
	opPrintJob             = 0x0002
	opGetJobs              = 0x000A
	opGetPrinterAttributes = 0x000B
)

// Delimiter tags.
const (
	tagOperation   = 0x01
	tagJob         = 0x02
	tagEnd         = 0x03
	tagPrinter     = 0x04
	tagUnsupported = 0x05
)

// Value tags.
const (
	tagInteger         = 0x21
	tagBoolean         = 0x22
	tagEnum            = 0x23
	tagRange           = 0x33
	tagBegCollection   = 0x34
	tagTextLang        = 0x35
	tagNameLang        = 0x36
	tagEndCollection   = 0x37
	tagText            = 0x41
	tagName            = 0x42
	tagKeyword         = 0x44
	tagURI             = 0x45
	tagCharset         = 0x47
	tagNaturalLanguage = 0x48
	tagMimeMediaType   = 0x49
	tagMemberAttrName  = 0x4A
)

// maxResponseBytes caps what a response may be. A printer's full attribute set
// is a few tens of KB.
const maxResponseBytes = 4 << 20

// Attr is one attribute with every value it carries. Integers and enums are
// int32, booleans bool, ranges [2]int32, text-like values string.
type Attr struct {
	Name   string
	Tag    byte
	Values []any
}

// Group is one attribute group (operation, job, printer, unsupported).
type Group struct {
	Tag   byte
	Attrs []Attr
}

// Message is a request (Code is the operation id) or a response (Code is the
// status code).
type Message struct {
	Major, Minor byte
	Code         uint16
	RequestID    uint32
	Groups       []Group
	// Document bytes after the attributes (requests only).
	Data []byte
}

// Get returns the first attribute with this name in a group with this tag.
func (m Message) Get(groupTag byte, name string) (Attr, bool) {
	for _, g := range m.Groups {
		if g.Tag != groupTag {
			continue
		}
		for _, a := range g.Attrs {
			if a.Name == name {
				return a, true
			}
		}
	}
	return Attr{}, false
}

func (a Attr) Str() string {
	if len(a.Values) == 0 {
		return ""
	}
	s, _ := a.Values[0].(string)
	return s
}

func (a Attr) Strs() []string {
	out := []string{}
	for _, v := range a.Values {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (a Attr) Int() (int32, bool) {
	if len(a.Values) == 0 {
		return 0, false
	}
	n, ok := a.Values[0].(int32)
	return n, ok
}

func (a Attr) Ints() []int32 {
	out := []int32{}
	for _, v := range a.Values {
		if n, ok := v.(int32); ok {
			out = append(out, n)
		}
	}
	return out
}

func (a Attr) Bool() (bool, bool) {
	if len(a.Values) == 0 {
		return false, false
	}
	b, ok := a.Values[0].(bool)
	return b, ok
}

// --- encoding --------------------------------------------------------------------------------

// Encode writes the message in IPP wire format.
func (m Message) Encode() ([]byte, error) {
	var b bytes.Buffer
	b.Write([]byte{m.Major, m.Minor})
	_ = binary.Write(&b, binary.BigEndian, m.Code)
	_ = binary.Write(&b, binary.BigEndian, m.RequestID)
	for _, g := range m.Groups {
		b.WriteByte(g.Tag)
		for _, a := range g.Attrs {
			if len(a.Values) == 0 {
				return nil, fmt.Errorf("ipp: attribute %s has no value", a.Name)
			}
			for i, v := range a.Values {
				name := a.Name
				if i > 0 {
					name = ""
				}
				val, err := encodeValue(a.Tag, v)
				if err != nil {
					return nil, fmt.Errorf("ipp: %s: %w", a.Name, err)
				}
				if len(name) > 0xFFFF || len(val) > 0xFFFF {
					return nil, fmt.Errorf("ipp: %s is too long", a.Name)
				}
				b.WriteByte(a.Tag)
				_ = binary.Write(&b, binary.BigEndian, uint16(len(name)))
				b.WriteString(name)
				_ = binary.Write(&b, binary.BigEndian, uint16(len(val)))
				b.Write(val)
			}
		}
	}
	b.WriteByte(tagEnd)
	b.Write(m.Data)
	return b.Bytes(), nil
}

func encodeValue(tag byte, v any) ([]byte, error) {
	switch tag {
	case tagInteger, tagEnum:
		n, ok := v.(int32)
		if !ok {
			return nil, errors.New("want int32")
		}
		return binary.BigEndian.AppendUint32(nil, uint32(n)), nil
	case tagBoolean:
		b, ok := v.(bool)
		if !ok {
			return nil, errors.New("want bool")
		}
		if b {
			return []byte{1}, nil
		}
		return []byte{0}, nil
	case tagRange:
		r, ok := v.([2]int32)
		if !ok {
			return nil, errors.New("want [2]int32")
		}
		out := binary.BigEndian.AppendUint32(nil, uint32(r[0]))
		return binary.BigEndian.AppendUint32(out, uint32(r[1])), nil
	default:
		s, ok := v.(string)
		if !ok {
			return nil, errors.New("want string")
		}
		return []byte(s), nil
	}
}

// --- decoding --------------------------------------------------------------------------------

var errShort = errors.New("ipp: message ends early")

// Decode reads a message. Members of collections are skipped: none of the
// attributes Palui reads are collections. Bytes after the end tag become Data.
func Decode(raw []byte) (Message, error) {
	r := bytes.NewReader(raw)
	var m Message
	head := make([]byte, 8)
	if _, err := io.ReadFull(r, head); err != nil {
		return m, errShort
	}
	m.Major, m.Minor = head[0], head[1]
	m.Code = binary.BigEndian.Uint16(head[2:4])
	m.RequestID = binary.BigEndian.Uint32(head[4:8])
	depth := 0
	for {
		tag, err := r.ReadByte()
		if err != nil {
			return m, errShort
		}
		if tag == tagEnd {
			rest, _ := io.ReadAll(r)
			m.Data = rest
			return m, nil
		}
		if tag < 0x10 {
			if depth != 0 {
				return m, errors.New("ipp: group starts inside a collection")
			}
			m.Groups = append(m.Groups, Group{Tag: tag})
			continue
		}
		name, err := readChunk(r)
		if err != nil {
			return m, err
		}
		val, err := readChunk(r)
		if err != nil {
			return m, err
		}
		switch tag {
		case tagBegCollection:
			depth++
			// A collection is still a value of the attribute: keep the name so
			// a later additional value lands on the right attribute.
			if depth == 1 && len(m.Groups) > 0 {
				addValue(&m.Groups[len(m.Groups)-1], string(name), tag, nil)
			}
			continue
		case tagEndCollection:
			if depth == 0 {
				return m, errors.New("ipp: collection ends without starting")
			}
			depth--
			continue
		}
		if depth > 0 {
			continue
		}
		if len(m.Groups) == 0 {
			return m, errors.New("ipp: attribute outside a group")
		}
		addValue(&m.Groups[len(m.Groups)-1], string(name), tag, decodeValue(tag, val))
	}
}

func readChunk(r *bytes.Reader) ([]byte, error) {
	var n uint16
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, errShort
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, errShort
	}
	return b, nil
}

func addValue(g *Group, name string, tag byte, v any) {
	if name == "" && len(g.Attrs) > 0 {
		last := &g.Attrs[len(g.Attrs)-1]
		if v != nil {
			last.Values = append(last.Values, v)
		}
		return
	}
	a := Attr{Name: name, Tag: tag}
	if v != nil {
		a.Values = []any{v}
	}
	g.Attrs = append(g.Attrs, a)
}

func decodeValue(tag byte, b []byte) any {
	switch tag {
	case tagInteger, tagEnum:
		if len(b) == 4 {
			return int32(binary.BigEndian.Uint32(b))
		}
	case tagBoolean:
		if len(b) == 1 {
			return b[0] != 0
		}
	case tagRange:
		if len(b) == 8 {
			return [2]int32{int32(binary.BigEndian.Uint32(b[:4])), int32(binary.BigEndian.Uint32(b[4:]))}
		}
	case tagTextLang, tagNameLang:
		// language length, language, text length, text
		if len(b) >= 2 {
			n := int(binary.BigEndian.Uint16(b))
			if len(b) >= 2+n+2 {
				m := int(binary.BigEndian.Uint16(b[2+n:]))
				if len(b) >= 4+n+m {
					return string(b[4+n : 4+n+m])
				}
			}
		}
	default:
		if tag >= 0x40 && tag <= 0x5F {
			return string(b)
		}
	}
	// Out-of-band values (unknown, no-value) and anything unexpected.
	return nil
}

// --- client ----------------------------------------------------------------------------------

// Client talks IPP to one printer or print queue.
type Client struct {
	// printer-uri as sent in requests (ipp:// or ipps://).
	printerURI string
	// Where the request is POSTed (http:// or https://).
	endpoint string
	// Host header override, "" for the URL's own host.
	host string
	hc   *http.Client
	next uint32
}

// NewClient parses the configured URL. ipp:// and ipps:// default to port 631.
func NewClient(rawURL string, hc *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("the printer URL %q isn't valid", rawURL)
	}
	var httpScheme, ippScheme, port string
	switch strings.ToLower(u.Scheme) {
	case "ipp":
		httpScheme, ippScheme, port = "http", "ipp", "631"
	case "ipps":
		httpScheme, ippScheme, port = "https", "ipps", "631"
	case "http":
		httpScheme, ippScheme, port = "http", "ipp", "80"
	case "https":
		httpScheme, ippScheme, port = "https", "ipps", "443"
	default:
		return nil, errors.New("the printer URL must start with ipp://, ipps://, http:// or https://")
	}
	if p := u.Port(); p != "" {
		port = p
	}
	hostname := u.Hostname()
	c := &Client{hc: hc}
	if c.hc == nil {
		c.hc = http.DefaultClient
	}
	// CUPS refuses a request that reaches it on loopback unless the Host header
	// says localhost (it answers 400 otherwise). From a container, Docker
	// Desktop and OrbStack deliver host.docker.internal to the host's
	// loopback, so name the host localhost in both the header and printer-uri.
	if strings.EqualFold(hostname, "host.docker.internal") {
		c.host = net.JoinHostPort("localhost", port)
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	c.endpoint = httpScheme + "://" + net.JoinHostPort(hostname, port) + path
	uriHost := net.JoinHostPort(hostname, port)
	if c.host != "" {
		uriHost = c.host
	}
	c.printerURI = ippScheme + "://" + uriHost + path
	return c, nil
}

// StatusError is a response whose status code isn't a success.
type StatusError struct {
	Code    uint16
	Message string
}

func (e *StatusError) Error() string {
	name := statusNames[e.Code]
	if name == "" {
		name = fmt.Sprintf("status 0x%04x", e.Code)
	}
	if e.Message != "" {
		return name + ": " + e.Message
	}
	return name
}

const (
	statusNotFound          = 0x0406
	statusFormatUnsupported = 0x040A
	statusNotAccepting      = 0x0506
)

var statusNames = map[uint16]string{
	0x0400:                  "client-error-bad-request",
	0x0401:                  "client-error-forbidden",
	0x0402:                  "client-error-not-authenticated",
	0x0403:                  "client-error-not-authorized",
	statusNotFound:          "client-error-not-found",
	0x0408:                  "client-error-request-entity-too-large",
	statusFormatUnsupported: "client-error-document-format-not-supported",
	0x040B:                  "client-error-attributes-or-values-not-supported",
	0x0500:                  "server-error-internal-error",
	0x0501:                  "server-error-operation-not-supported",
	0x0504:                  "server-error-device-error",
	0x0505:                  "server-error-temporary-error",
	statusNotAccepting:      "server-error-not-accepting-jobs",
	0x0507:                  "server-error-busy",
}

func (c *Client) operationGroup(extra ...Attr) Group {
	attrs := []Attr{
		{Name: "attributes-charset", Tag: tagCharset, Values: []any{"utf-8"}},
		{Name: "attributes-natural-language", Tag: tagNaturalLanguage, Values: []any{"en"}},
		{Name: "printer-uri", Tag: tagURI, Values: []any{c.printerURI}},
		{Name: "requesting-user-name", Tag: tagName, Values: []any{"palui"}},
	}
	return Group{Tag: tagOperation, Attrs: append(attrs, extra...)}
}

// Do sends one request and returns the response, or a *StatusError when the
// printer refused it.
func (c *Client) Do(ctx context.Context, op uint16, groups []Group, data []byte) (Message, error) {
	c.next++
	req := Message{Major: 1, Minor: 1, Code: op, RequestID: c.next, Groups: groups, Data: data}
	body, err := req.Encode()
	if err != nil {
		return Message{}, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	hr.Header.Set("Content-Type", "application/ipp")
	if c.host != "" {
		hr.Host = c.host
	}
	res, err := c.hc.Do(hr)
	if err != nil {
		return Message{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Message{}, fmt.Errorf("the printer answered HTTP %d", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil {
		return Message{}, err
	}
	if len(raw) > maxResponseBytes {
		return Message{}, errors.New("the printer's answer is too big")
	}
	out, err := Decode(raw)
	if err != nil {
		return Message{}, err
	}
	if out.Code >= 0x0100 {
		msg, _ := out.Get(tagOperation, "status-message")
		return out, &StatusError{Code: out.Code, Message: msg.Str()}
	}
	return out, nil
}

// PrinterInfo is the part of Get-Printer-Attributes Palui shows.
type PrinterInfo struct {
	Name           string
	MakeAndModel   string
	State          string
	Reasons        []string
	Message        string
	AcceptingJobs  bool
	QueuedJobs     int32
	Formats        []string
	ColorSupported bool
	Markers        []Marker
}

// Marker is one ink or toner level, 0-100. The printer may not know (-2).
type Marker struct {
	Name  string
	Level int32
}

var printerStates = map[int32]string{3: "idle", 4: "processing", 5: "stopped"}

var printerAttrs = []any{
	"printer-name", "printer-make-and-model", "printer-state", "printer-state-reasons", "printer-state-message",
	"printer-is-accepting-jobs", "queued-job-count", "document-format-supported", "color-supported",
	"marker-names", "marker-levels",
}

// Printer reads the printer's (or the queue's) state.
func (c *Client) Printer(ctx context.Context) (PrinterInfo, error) {
	res, err := c.Do(ctx, opGetPrinterAttributes, []Group{c.operationGroup(
		Attr{Name: "requested-attributes", Tag: tagKeyword, Values: printerAttrs},
	)}, nil)
	if err != nil {
		return PrinterInfo{}, err
	}
	get := func(name string) Attr { a, _ := res.Get(tagPrinter, name); return a }
	info := PrinterInfo{
		Name:         get("printer-name").Str(),
		MakeAndModel: get("printer-make-and-model").Str(),
		Message:      get("printer-state-message").Str(),
		Formats:      get("document-format-supported").Strs(),
		Reasons:      []string{},
	}
	if n, ok := get("printer-state").Int(); ok {
		info.State = printerStates[n]
	}
	for _, r := range get("printer-state-reasons").Strs() {
		if r != "none" {
			info.Reasons = append(info.Reasons, r)
		}
	}
	info.AcceptingJobs, _ = get("printer-is-accepting-jobs").Bool()
	info.QueuedJobs, _ = get("queued-job-count").Int()
	info.ColorSupported, _ = get("color-supported").Bool()
	names, levels := get("marker-names").Strs(), get("marker-levels").Ints()
	for i, n := range names {
		if i < len(levels) {
			info.Markers = append(info.Markers, Marker{Name: n, Level: levels[i]})
		}
	}
	return info, nil
}

// JobInfo is one job.
type JobInfo struct {
	ID    int32
	Name  string
	State string
}

var jobStates = map[int32]string{3: "pending", 4: "held", 5: "processing", 6: "stopped", 7: "canceled", 8: "aborted", 9: "completed"}

// Jobs lists the jobs that haven't finished yet, at most limit.
func (c *Client) Jobs(ctx context.Context, limit int32) ([]JobInfo, error) {
	res, err := c.Do(ctx, opGetJobs, []Group{c.operationGroup(
		Attr{Name: "limit", Tag: tagInteger, Values: []any{limit}},
		Attr{Name: "which-jobs", Tag: tagKeyword, Values: []any{"not-completed"}},
		Attr{Name: "requested-attributes", Tag: tagKeyword, Values: []any{"job-id", "job-name", "job-state"}},
	)}, nil)
	if err != nil {
		return nil, err
	}
	out := []JobInfo{}
	for _, g := range res.Groups {
		if g.Tag != tagJob {
			continue
		}
		var j JobInfo
		for _, a := range g.Attrs {
			switch a.Name {
			case "job-id":
				j.ID, _ = a.Int()
			case "job-name":
				j.Name = a.Str()
			case "job-state":
				n, _ := a.Int()
				j.State = jobStates[n]
			}
		}
		out = append(out, j)
	}
	return out, nil
}

// Job is one document to print with its options. Zero values leave the
// printer's default.
type Job struct {
	Name     string
	Format   string
	Data     []byte
	Copies   int32
	Media    string
	Color    string // "color" or "monochrome"
	Pages    [][2]int32
	FitScale bool
}

// Print sends a Print-Job and returns the new job.
func (c *Client) Print(ctx context.Context, j Job) (JobInfo, error) {
	op := c.operationGroup(
		Attr{Name: "job-name", Tag: tagName, Values: []any{j.Name}},
		Attr{Name: "document-format", Tag: tagMimeMediaType, Values: []any{j.Format}},
	)
	job := Group{Tag: tagJob}
	if j.Copies > 1 {
		job.Attrs = append(job.Attrs, Attr{Name: "copies", Tag: tagInteger, Values: []any{j.Copies}})
	}
	if j.Media != "" {
		job.Attrs = append(job.Attrs, Attr{Name: "media", Tag: tagKeyword, Values: []any{j.Media}})
	}
	if j.Color != "" {
		job.Attrs = append(job.Attrs, Attr{Name: "print-color-mode", Tag: tagKeyword, Values: []any{j.Color}})
	}
	if len(j.Pages) > 0 {
		vals := make([]any, len(j.Pages))
		for i, p := range j.Pages {
			vals[i] = p
		}
		job.Attrs = append(job.Attrs, Attr{Name: "page-ranges", Tag: tagRange, Values: vals})
	}
	if j.FitScale {
		job.Attrs = append(job.Attrs, Attr{Name: "print-scaling", Tag: tagKeyword, Values: []any{"fit"}})
	}
	groups := []Group{op}
	if len(job.Attrs) > 0 {
		groups = append(groups, job)
	}
	res, err := c.Do(ctx, opPrintJob, groups, j.Data)
	if err != nil {
		return JobInfo{}, err
	}
	out := JobInfo{Name: j.Name}
	if a, ok := res.Get(tagJob, "job-id"); ok {
		out.ID, _ = a.Int()
	}
	if a, ok := res.Get(tagJob, "job-state"); ok {
		n, _ := a.Int()
		out.State = jobStates[n]
	}
	return out, nil
}
