// Package printer is the printer connector: print a file from the chat on a
// printer or print queue over IPP. Point it at a CUPS queue (macOS, Linux)
// rather than the printer itself: many home printers only take raster formats,
// and CUPS turns a PDF or a photo into what the printer understands.
package printer

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// URLPlaceholder is the shape of a CUPS queue URL as seen from Palui's container.
const URLPlaceholder = "ipp://host.docker.internal:631/printers/My_Printer"

// ConfigZod is the printer config: where the queue answers.
var ConfigZod = zod.Object(
	zod.F("url", zod.String().URL(regexp.MustCompile(`^(ipps?|https?)$`))),
)

var Manifest = connectors.Manifest{
	ID:       "printer",
	Kind:     "tools",
	Name:     "Printer",
	Icon:     "printer",
	Summary:  "Print files from chat on your printer.",
	Multiple: true,
	Trust:    "own",
	Setup: []connectors.SetupStep{
		{Field: "url", Label: "Printer URL", Placeholder: URLPlaceholder, Help: "A CUPS queue on the Mac works best. lpstat -p lists the queue names.", Command: "lpstat -p -d"},
	},
	PauseNote:     "While paused, Palui can't print. Jobs already sent keep printing.",
	RemoveWarning: "This removes the printer from Palui. The printer and its queue aren't touched.",
	Docs: &connectors.Docs{
		Commands: []string{"lpstat -p -d"},
		Notes: []string{
			"Prints PDFs, JPEGs and PNGs sent in the chat or saved as attachments.",
			"Use the Mac's CUPS queue, not the printer's own address: many printers only take raster formats, and CUPS converts the file for them.",
			"Printing is a write tool: it runs right away in your own chat, and asks first after untrusted content or in a scheduled job.",
		},
	},
	ConfigZod:   ConfigZod,
	ParseConfig: kit.ParseConfig(ConfigZod),
}

// Config is the parsed config.
type Config struct{ URL string }

// ParseConfig reads the stored config; ok is false when it doesn't parse.
func ParseConfig(raw *jsonx.Object) (Config, bool) {
	if raw == nil {
		return Config{}, false
	}
	out, err := zod.Parse(ConfigZod, raw)
	if err != nil {
		return Config{}, false
	}
	v, _ := out.(*jsonx.Object).Get("url")
	s, _ := v.(string)
	return Config{URL: strx.Trim(s)}, true
}

// formats are the document types Palui sends, by normalized MIME type.
var formats = map[string]string{
	"application/pdf": "PDF",
	"image/jpeg":      "JPEG",
	"image/png":       "PNG",
}

// paperSizes maps the paper the model may ask for to PWG media names, which
// both CUPS and IPP Everywhere printers understand.
var paperSizes = map[string]string{
	"A4":     "iso_a4_210x297mm",
	"A5":     "iso_a5_148x210mm",
	"A6":     "iso_a6_105x148mm",
	"Letter": "na_letter_8.5x11in",
	"Legal":  "na_legal_8.5x14in",
	"4x6":    "na_index-4x6_4x6in",
}

const (
	maxCopies = 20
	maxJobs   = 10
)

var pagesRe = regexp.MustCompile(`^\d+(-\d+)?(,\d+(-\d+)?)*$`)

var (
	statusInput = zod.Object()
	printInput  = zod.Object(
		zod.F("attachment_id", zod.String().Min(1).Optional().Describe("A saved file's id from list_attachments. Omit to print a file sent in the chat.")),
		zod.F("nomor", zod.Int().Min(1).Optional().Describe("Which file, 1-based. In the current message: in the order they were sent. With `earlier`: the number from the list, 1 being the most recent. Omit when there is only one.")),
		zod.F("earlier", zod.Bool().Optional().Describe(fmt.Sprintf("Print a file from an earlier message in this chat instead of the current one: the last 24 hours, at most %d files, newest first.", attachments.RecentFilesMax))),
		zod.F("copies", zod.Int().Min(1).Max(maxCopies).Optional().Describe("How many copies. Default 1.")),
		zod.F("color", zod.Enum("color", "gray").Optional().Describe("gray saves color ink. Default: the printer's own setting.")),
		zod.F("paper", zod.Enum("A4", "A5", "A6", "Letter", "Legal", "4x6").Optional().Describe("Paper size. Default: the printer's own paper.")),
		zod.F("pages", zod.String().Regex(pagesRe, `^\d+(-\d+)?(,\d+(-\d+)?)*$`, "Use page numbers and ranges, e.g. 1-3,5").Optional().Describe("PDF pages to print, e.g. 1-3,5. Default: all.")),
		zod.F("fit_to_page", zod.Bool().Optional().Describe("Scale the file to fit the paper. Useful for photos and screenshots.")),
	)
)

// Options is the runtime's injectable plumbing.
type Options struct {
	Client *http.Client
	// Key returns MASTER_KEY, for reading saved attachments.
	Key func() string
}

func (o Options) key() string {
	if o.Key != nil {
		return o.Key()
	}
	k, _ := config.MasterKey(config.Get())
	return k
}

func (o Options) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// ErrorText is what the owner and the model see when the printer can't be used.
func ErrorText(rawURL string, err error) string {
	var se *StatusError
	if errors.As(err, &se) {
		switch se.Code {
		case statusNotFound:
			return "The printer queue wasn't found. Check the queue name in the URL (lpstat -p on the Mac lists them)."
		case statusNotAccepting:
			return "The printer isn't accepting jobs right now. Check whether its queue is paused on the Mac."
		case statusFormatUnsupported:
			return "The printer can't take this file type directly. Point the connector at the Mac's CUPS queue (" + URLPlaceholder + ") so macOS converts the file."
		}
		return "The printer refused the request: " + se.Error()
	}
	var ne net.Error
	if errors.As(err, &ne) || strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "no such host") {
		return fmt.Sprintf("Can't reach the printer at %s. Check the URL, and that the Mac and CUPS are up.", rawURL)
	}
	return err.Error()
}

// supports reports whether the printer takes the format. A printer that only
// lists application/octet-stream doesn't really render PDFs or photos.
func supports(info PrinterInfo, format string) bool {
	return slices.Contains(info.Formats, format)
}

func supportedNames(info PrinterInfo) []string {
	var out []string
	for _, f := range []string{"application/pdf", "image/jpeg", "image/png"} {
		if supports(info, f) {
			out = append(out, formats[f])
		}
	}
	return out
}

// Test checks the printer answers and takes at least one format Palui sends.
func Test(ctx context.Context, cfg Config, hc *http.Client) connectors.TestResult {
	client, err := NewClient(cfg.URL, hc)
	if err != nil {
		return connectors.TestResult{OK: false, Detail: err.Error()}
	}
	info, err := client.Printer(ctx)
	if err != nil {
		return connectors.TestResult{OK: false, Detail: ErrorText(cfg.URL, err)}
	}
	name := info.MakeAndModel
	if name == "" {
		name = info.Name
	}
	if name == "" {
		name = "the printer"
	}
	takes := supportedNames(info)
	if len(takes) == 0 {
		return connectors.TestResult{OK: false, Detail: fmt.Sprintf("Connected to %s, but it doesn't take PDF, JPEG or PNG (it lists %s). Point the connector at the Mac's CUPS queue instead, e.g. %s.", name, strings.Join(info.Formats, ", "), URLPlaceholder)}
	}
	state := info.State
	if state == "" {
		state = "unknown"
	}
	return connectors.TestResult{OK: true, Detail: fmt.Sprintf("Connected to %s (%s). Prints %s.", name, state, strings.Join(takes, ", "))}
}

func parsePages(s string) ([][2]int32, error) {
	if s == "" {
		return nil, nil
	}
	var out [][2]int32
	last := int32(0)
	for _, part := range strings.Split(s, ",") {
		a, b, isRange := strings.Cut(part, "-")
		lo, err1 := strconv.ParseInt(a, 10, 32)
		hi := lo
		var err2 error
		if isRange {
			hi, err2 = strconv.ParseInt(b, 10, 32)
		}
		if err1 != nil || err2 != nil || lo < 1 || hi < lo {
			return nil, fmt.Errorf("pages %q isn't a valid range. Use page numbers and ranges, e.g. 1-3,5", part)
		}
		if int32(lo) <= last {
			return nil, errors.New("list pages in order without overlaps, e.g. 1-3,5")
		}
		out = append(out, [2]int32{int32(lo), int32(hi)})
		last = int32(hi)
	}
	return out, nil
}

// file is what gets printed.
type file struct {
	name, mime string
	data       []byte
}

func describe(c attachments.Candidate) any {
	var filename any
	if c.Filename != nil {
		filename = *c.Filename
	}
	return kit.O("nomor", c.Index, "kind", c.Kind, "mimeType", c.MimeType, "filename", filename, "size", attachments.FormatBytes(c.Size))
}

func describeRecent(list []attachments.RecentCandidate) []any {
	out := make([]any, len(list))
	for i, c := range list {
		o := describe(c.Candidate).(*jsonx.Object)
		o.Set("sentAt", kit.ISO(c.SentAt))
		o.Set("caption", c.Caption)
		out[i] = o
	}
	return out
}

func describeAll(list []attachments.Candidate) []any {
	out := make([]any, len(list))
	for i, c := range list {
		out[i] = describe(c)
	}
	return out
}

func fromCandidate(c attachments.Candidate) file {
	name := "file"
	if c.Filename != nil && *c.Filename != "" {
		name = *c.Filename
	} else if c.Kind == db.MediaImage {
		name = "photo"
	}
	b, _ := base64.StdEncoding.DecodeString(c.Data)
	if b == nil {
		b, _ = base64.RawStdEncoding.DecodeString(strings.TrimRight(c.Data, "="))
	}
	return file{name: name, mime: c.MimeType, data: b}
}

// pick finds the file to print, or a refusal (as a tool result) to return.
func pick(d *db.DB, turn connectors.TurnInfo, in kit.Input, key string) (file, any, error) {
	if id := strx.Trim(in.S("attachment_id")); id != "" {
		meta, ok, err := attachments.Get(d, id)
		if err != nil {
			return file{}, nil, err
		}
		if !ok || meta.ChatID != turn.ChatID {
			return file{}, kit.Fail(fmt.Sprintf("No saved attachment %s in this chat. Call list_attachments to get the ids.", id)), nil
		}
		f, _, err := attachments.Read(d, id, key)
		if err != nil {
			return file{}, nil, err
		}
		return file{name: attachments.DownloadName(meta.Filename, meta.Title, meta.MimeType), mime: attachments.NormalizeMime(meta.MimeType), data: f.Bytes}, nil, nil
	}
	nomor, hasNomor := in.Int("nomor")
	pickN := int64(1)
	if hasNomor {
		pickN = nomor
	}
	if in.True("earlier") {
		recent, err := attachments.RecentCandidates(d, turn.ChatID, turn.Now)
		if err != nil {
			return file{}, nil, err
		}
		if len(recent) == 0 {
			return file{}, kit.Fail("No image or document was sent in this chat in the last 24 hours. Ask the owner to send it again."), nil
		}
		if !hasNomor && len(recent) > 1 {
			return file{}, kit.O("ok", false, "error", "Pick one: call again with `earlier: true` and its `nomor`. Match it by caption and time; ask the owner if it's still unclear.", "files", describeRecent(recent)), nil
		}
		if pickN > int64(len(recent)) {
			return file{}, kit.O("ok", false, "error", fmt.Sprintf("There is no earlier file number %d.", pickN), "files", describeRecent(recent)), nil
		}
		return fromCandidate(recent[pickN-1].Candidate), nil, nil
	}
	runID := turn.BatchRunID
	if runID == "" {
		runID = turn.RunID
	}
	candidates, err := attachments.TurnCandidates(d, turn.ChatID, runID)
	if err != nil {
		return file{}, nil, err
	}
	if len(candidates) == 0 {
		recent, err := attachments.RecentCandidates(d, turn.ChatID, turn.Now)
		if err != nil {
			return file{}, nil, err
		}
		if len(recent) > 0 {
			return file{}, kit.O("ok", false, "error", "The current message has no file. To print one from an earlier message, call again with `earlier: true` and the `nomor` of the right file below.", "files", describeRecent(recent)), nil
		}
		return file{}, kit.Fail("The current message has no file to print, and none was sent in this chat in the last 24 hours. A saved file can be printed with its attachment_id."), nil
	}
	if !hasNomor && len(candidates) > 1 {
		return file{}, kit.O("ok", false, "error", "The current message has more than one file. Call again with `nomor`.", "files", describeAll(candidates)), nil
	}
	if pickN > int64(len(candidates)) {
		return file{}, kit.O("ok", false, "error", fmt.Sprintf("There is no file number %d.", pickN), "files", describeAll(candidates)), nil
	}
	return fromCandidate(candidates[pickN-1]), nil, nil
}

// Tools are the printer's tools for one turn.
func Tools(d *db.DB, turn connectors.TurnInfo, cfg Config, opts Options) *connectors.ToolSet {
	hc := opts.client()
	set := connectors.NewToolSet()
	set.Set("printer_status", connectors.PaluiTool{
		Data:  connectors.DataNone,
		Class: "read",
		Title: "Check the printer",
		Tool: kit.Tool("Check the printer: its state (idle, processing, stopped), any problem it reports (out of paper, jam, low ink), ink levels when it knows them, and the jobs still waiting. It reads the print queue: a printer that's off or asleep can still look idle until a job is sent.", statusInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				client, err := NewClient(cfg.URL, hc)
				if err != nil {
					return kit.Fail(err.Error()), nil
				}
				info, err := client.Printer(ctx)
				if err != nil {
					return kit.Fail(ErrorText(cfg.URL, err)), nil
				}
				jobs, err := client.Jobs(ctx, maxJobs)
				if err != nil {
					jobs = nil
				}
				jobList := make([]any, len(jobs))
				for i, j := range jobs {
					jobList[i] = kit.O("id", j.ID, "name", j.Name, "state", j.State)
				}
				var ink []any
				for _, m := range info.Markers {
					if m.Level >= 0 {
						ink = append(ink, kit.O("name", m.Name, "percent", m.Level))
					}
				}
				return kit.O("ok", true, "printer", kit.If(info.MakeAndModel != "", info.MakeAndModel), "state", info.State,
					"problems", info.Reasons, "message", kit.If(info.Message != "", info.Message),
					"acceptingJobs", info.AcceptingJobs, "prints", supportedNames(info),
					"ink", kit.If(len(ink) > 0, ink), "jobs", jobList), nil
			}),
	})
	set.Set("print_file", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "write",
		Title: "Print a file",
		Tool: kit.Tool("Print a PDF, JPEG or PNG on the printer. By default it prints the file in the current message. A file from an earlier message can be printed with `earlier: true` (call it without `nomor` first to see the recent files), and a saved file with its attachment_id. Only print when the owner asks. Never tell the owner it printed unless this tool returned ok, and even then say it was sent to the printer: ask them to check the tray.", printInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				if turn.ChatID == "" {
					return kit.Fail("There is no chat to print from in this run."), nil
				}
				pages, err := parsePages(strx.Trim(in.S("pages")))
				if err != nil {
					return kit.Fail(err.Error()), nil
				}
				f, refusal, err := pick(d, turn, in, opts.key())
				if err != nil || refusal != nil {
					return refusal, err
				}
				label, ok := formats[f.mime]
				if !ok {
					return kit.Fail(fmt.Sprintf("Can't print a %s file. Only PDF, JPEG and PNG can be printed.", f.mime)), nil
				}
				if len(f.data) == 0 {
					return kit.Fail("The file is empty."), nil
				}
				client, err := NewClient(cfg.URL, hc)
				if err != nil {
					return kit.Fail(err.Error()), nil
				}
				info, err := client.Printer(ctx)
				if err != nil {
					return kit.Fail(ErrorText(cfg.URL, err)), nil
				}
				if !supports(info, f.mime) {
					return kit.Fail(fmt.Sprintf("This printer can't take %s files directly. Ask the owner to point the printer connector at the Mac's CUPS queue.", label)), nil
				}
				if !info.AcceptingJobs {
					return kit.Fail("The printer isn't accepting jobs right now. Ask the owner to check whether its queue is paused."), nil
				}
				job := Job{Name: strx.Slice16(f.name, 200), Format: f.mime, Data: f.data, Pages: pages, FitScale: in.True("fit_to_page")}
				if n, ok := in.Int("copies"); ok {
					job.Copies = int32(n)
				}
				if p := in.S("paper"); p != "" {
					job.Media = paperSizes[p]
				}
				switch in.S("color") {
				case "gray":
					job.Color = "monochrome"
				case "color":
					job.Color = "color"
				}
				res, err := client.Print(ctx, job)
				if err != nil {
					return kit.Fail(ErrorText(cfg.URL, err)), nil
				}
				return kit.O("ok", true, "jobId", res.ID, "jobState", res.State, "file", f.name, "problems", kit.If(len(info.Reasons) > 0, info.Reasons),
					"message", fmt.Sprintf("Oke, \"%s\" udah aku kirim ke printer. Cek kertasnya ya.", f.name)), nil
			}),
	})
	return set
}

// New is the runtime with injectable plumbing.
func New(opts Options) *connectors.Runtime {
	return &connectors.Runtime{
		Manifest: Manifest,
		Test: func(c connectors.Context) (connectors.TestResult, error) {
			cfg, ok := ParseConfig(c.Config)
			if !ok {
				return connectors.TestResult{OK: false, Detail: "Set the printer URL first, e.g. " + URLPlaceholder + "."}, nil
			}
			ctx := c.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			return Test(ctx, cfg, opts.client()), nil
		},
		Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
			cfg, ok := ParseConfig(c.Config)
			if !ok {
				return nil, errors.New("Set the printer URL first, e.g. " + URLPlaceholder + ".")
			}
			if _, err := NewClient(cfg.URL, nil); err != nil {
				return nil, err
			}
			return &connectors.RunningConnector{
				Stop: func(context.Context) error { return nil },
				Tools: func(_ context.Context, turn connectors.TurnInfo) (*connectors.ToolSet, error) {
					return Tools(c.DB, turn, cfg, opts), nil
				},
			}, nil
		},
	}
}

// Runtime is the printer connector runtime.
var Runtime = New(Options{})
