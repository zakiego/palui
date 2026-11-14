package channelutil

// Markdown from the model, converted to what each channel renders.
//
// Telegram: HTML parse mode, which only accepts a small tag set (b, i, u, s,
// code, pre, a, blockquote) and rejects the whole message on a stray "<" or
// "&". So all text is escaped first and markdown markers are turned into tags
// afterwards. WhatsApp: *bold*, _italic_, ~strike~, ```mono```, `inline`. Links
// become text.
//
// Fenced code is never rewritten in either converter. The TS regexes use
// lookarounds RE2 doesn't have, so each one is a small hand-written matcher
// with the same backtracking result (JS \s, ASCII \w, "." stopping at line
// terminators).

import (
	"strconv"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/strx"
)

type segment struct {
	code bool
	lang string
	body string
}

func isWord(r rune) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// isLineTerm: what JS "." refuses besides "\n".
func isLineTerm(r rune) bool { return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 }

func skipSpace(rs []rune, i int) int {
	for i < len(rs) && strx.IsSpace(rs[i]) {
		i++
	}
	return i
}

func hasTriple(rs []rune, i int) bool {
	return len(rs)-i >= 3 && rs[i] == '`' && rs[i+1] == '`' && rs[i+2] == '`'
}

// fenceLine is /^\s*```\s*([\w+#.-]*)\s*$/ falling back to /^\s*```/ (lang "").
func fenceLine(line string) (lang string, ok bool) {
	rs := []rune(line)
	i := skipSpace(rs, 0)
	if !hasTriple(rs, i) {
		return "", false
	}
	j := skipSpace(rs, i+3)
	k := j
	for k < len(rs) && (isWord(rs[k]) || rs[k] == '+' || rs[k] == '#' || rs[k] == '.' || rs[k] == '-') {
		k++
	}
	if skipSpace(rs, k) == len(rs) {
		return string(rs[j:k]), true
	}
	return "", true
}

// splitFences splits into prose and fenced code runs. An unclosed fence runs
// to the end.
func splitFences(md string) []segment {
	var out []segment
	var buf []string
	inFence := false
	lang := ""
	flush := func(code bool) {
		if len(buf) > 0 || code {
			out = append(out, segment{code: code, lang: lang, body: strings.Join(buf, "\n")})
		}
		buf = nil
	}
	for _, line := range strings.Split(md, "\n") {
		if l, ok := fenceLine(line); ok {
			if inFence {
				flush(true)
				inFence = false
				lang = ""
			} else {
				flush(false)
				inFence = true
				lang = l
			}
			continue
		}
		buf = append(buf, line)
	}
	flush(inFence)
	return out
}

const ph0 = '\u0000'

type saver struct{ saved []string }

func (s *saver) ph(v string) string {
	s.saved = append(s.saved, v)
	return "\u0000" + strconv.Itoa(len(s.saved)-1) + "\u0000"
}

// restore is s.replace(/\0(\d+)\0/g, saved[i] ?? "").
func (s *saver) restore(str string) string {
	rs := []rune(str)
	var b strings.Builder
	for i := 0; i < len(rs); {
		if rs[i] == ph0 {
			j := i + 1
			for j < len(rs) && rs[j] >= '0' && rs[j] <= '9' {
				j++
			}
			if j > i+1 && j < len(rs) && rs[j] == ph0 {
				if n, err := strconv.Atoi(string(rs[i+1 : j])); err == nil && n >= 0 && n < len(s.saved) {
					b.WriteString(s.saved[n])
				}
				i = j + 1
				continue
			}
		}
		b.WriteRune(rs[i])
		i++
	}
	return b.String()
}

// EscapeHTML is escapeHtml.
func EscapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}

func safeHref(url string) (string, bool) {
	u := strx.Trim(url)
	lower := strings.ToLower(u)
	if !(strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "tg://") || strings.HasPrefix(lower, "mailto:")) {
		return "", false
	}
	return strings.ReplaceAll(EscapeHTML(u), `"`, "&quot;"), true
}

// replaceInlineCode is s.replace(/`([^`\n]+)`/g, fn(match, code)).
func replaceInlineCode(s string, fn func(match, code string) string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); {
		if rs[i] == '`' {
			j := i + 1
			for j < len(rs) && rs[j] != '`' && rs[j] != '\n' {
				j++
			}
			if j > i+1 && j < len(rs) && rs[j] == '`' {
				b.WriteString(fn(string(rs[i:j+1]), string(rs[i+1:j])))
				i = j + 1
				continue
			}
		}
		b.WriteRune(rs[i])
		i++
	}
	return b.String()
}

// replaceLinks is s.replace(/\[([^\]\n]+)\]\(([^)\s]+)\)/g, fn(match, label, url)).
func replaceLinks(s string, fn func(match, label, url string) string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); {
		if rs[i] == '[' {
			j := i + 1
			for j < len(rs) && rs[j] != ']' && rs[j] != '\n' {
				j++
			}
			if j > i+1 && j+1 < len(rs) && rs[j] == ']' && rs[j+1] == '(' {
				k := j + 2
				for k < len(rs) && rs[k] != ')' && !strx.IsSpace(rs[k]) {
					k++
				}
				if k > j+2 && k < len(rs) && rs[k] == ')' {
					b.WriteString(fn(string(rs[i:k+1]), string(rs[i+1:j]), string(rs[j+2:k])))
					i = k + 1
					continue
				}
			}
		}
		b.WriteRune(rs[i])
		i++
	}
	return b.String()
}

// emph is one emphasis regex:
//
//	M(?=\S)([^X\n]*?\S)M                      (pre nil)
//	(^|[^\w<pre>])M(?=\S)([^X\n]*?\S)M(?![\w<after>])
//
// replaced by open + body + close (the prefix char kept).
type emph struct {
	marker    []rune
	excl      rune
	prefixed  bool
	preExcl   rune // 0: [^\w]
	afterExcl rune // 0: (?!\w)
	open      string
	close     string
}

func (e emph) markerAt(rs []rune, i int) bool {
	if len(rs)-i < len(e.marker) {
		return false
	}
	for j, m := range e.marker {
		if rs[i+j] != m {
			return false
		}
	}
	return true
}

// body finds the lazy body after the opening marker (at p). It returns the
// body end (exclusive) and the match end.
func (e emph) body(rs []rune, p int) (int, int, bool) {
	if p >= len(rs) || strx.IsSpace(rs[p]) {
		return 0, 0, false
	}
	for q := p; q < len(rs); q++ {
		if q > p && (rs[q-1] == e.excl || rs[q-1] == '\n') {
			return 0, 0, false
		}
		if strx.IsSpace(rs[q]) || !e.markerAt(rs, q+1) {
			continue
		}
		end := q + 1 + len(e.marker)
		if e.prefixed && end < len(rs) && (isWord(rs[end]) || (e.afterExcl != 0 && rs[end] == e.afterExcl)) {
			continue
		}
		return q + 1, end, true
	}
	return 0, 0, false
}

func (e emph) apply(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); {
		if !e.prefixed {
			if e.markerAt(rs, i) {
				p := i + len(e.marker)
				if bodyEnd, end, ok := e.body(rs, p); ok {
					b.WriteString(e.open + string(rs[p:bodyEnd]) + e.close)
					i = end
					continue
				}
			}
		} else {
			// ^ first (only at 0), then one char of [^\w<pre>].
			if i == 0 && e.markerAt(rs, 0) {
				p := len(e.marker)
				if bodyEnd, end, ok := e.body(rs, p); ok {
					b.WriteString(e.open + string(rs[p:bodyEnd]) + e.close)
					i = end
					continue
				}
			}
			if !isWord(rs[i]) && !(e.preExcl != 0 && rs[i] == e.preExcl) && e.markerAt(rs, i+1) {
				p := i + 1 + len(e.marker)
				if bodyEnd, end, ok := e.body(rs, p); ok {
					b.WriteString(string(rs[i]) + e.open + string(rs[p:bodyEnd]) + e.close)
					i = end
					continue
				}
			}
		}
		b.WriteRune(rs[i])
		i++
	}
	return b.String()
}

func inlineToHTML(line string, sv *saver) string {
	// Inline code first, so markers inside it are left alone.
	s := replaceInlineCode(line, func(_, code string) string { return sv.ph("<code>" + EscapeHTML(code) + "</code>") })
	// Links: [text](url). The text is formatted later with the rest of the line.
	s = replaceLinks(s, func(m, label, url string) string {
		href, ok := safeHref(url)
		if !ok {
			return m
		}
		open := sv.ph(`<a href="` + href + `">`)
		close := sv.ph("</a>")
		return open + label + close
	})
	s = EscapeHTML(s)
	s = emph{marker: []rune("**"), excl: '*', open: "<b>", close: "</b>"}.apply(s)
	s = emph{marker: []rune("__"), excl: '_', open: "<b>", close: "</b>"}.apply(s)
	s = emph{marker: []rune("~~"), excl: '~', open: "<s>", close: "</s>"}.apply(s)
	s = emph{marker: []rune("*"), excl: '*', prefixed: true, preExcl: '*', afterExcl: '*', open: "<i>", close: "</i>"}.apply(s)
	s = emph{marker: []rune("_"), excl: '_', prefixed: true, open: "<i>", close: "</i>"}.apply(s)
	return s
}

// headingOf is /^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$/.
func headingOf(raw string) (string, bool) {
	rs := []rune(raw)
	i := skipSpace(rs, 0)
	if i > 3 {
		return "", false
	}
	h := i
	for h < len(rs) && rs[h] == '#' {
		h++
	}
	if h == i || h-i > 6 || h >= len(rs) || !strx.IsSpace(rs[h]) {
		return "", false
	}
	rest := rs[skipSpace(rs, h):]
	tail := func(t []rune) bool {
		j := skipSpace(t, 0)
		for j < len(t) && t[j] == '#' {
			j++
		}
		return skipSpace(t, j) == len(t)
	}
	for k := 0; k <= len(rest); k++ {
		if k > 0 && isLineTerm(rest[k-1]) {
			return "", false
		}
		if tail(rest[k:]) {
			return string(rest[:k]), true
		}
	}
	return "", false
}

func noLineTerm(rs []rune) bool {
	for _, r := range rs {
		if isLineTerm(r) {
			return false
		}
	}
	return true
}

// bulletOf is /^(\s*)[<markers>]\s+(.*)$/.
func bulletOf(raw, markers string) (lead, rest string, ok bool) {
	rs := []rune(raw)
	i := skipSpace(rs, 0)
	if i >= len(rs) || !strings.ContainsRune(markers, rs[i]) || i+1 >= len(rs) || !strx.IsSpace(rs[i+1]) {
		return "", "", false
	}
	j := skipSpace(rs, i+1)
	if !noLineTerm(rs[j:]) {
		return "", "", false
	}
	return string(rs[:i]), string(rs[j:]), true
}

// quoteOf is /^\s*>\s?(.*)$/.
func quoteLine(raw string) (string, bool) {
	rs := []rune(raw)
	i := skipSpace(rs, 0)
	if i >= len(rs) || rs[i] != '>' {
		return "", false
	}
	j := i + 1
	if j < len(rs) && strx.IsSpace(rs[j]) {
		if noLineTerm(rs[j+1:]) {
			return string(rs[j+1:]), true
		}
	}
	if noLineTerm(rs[j:]) {
		return string(rs[j:]), true
	}
	return "", false
}

// isRule is /^\s*([-*_])(\s*\1){2,}\s*$/.
func isRule(raw string) bool {
	var c rune
	n := 0
	for _, r := range raw {
		if strx.IsSpace(r) {
			continue
		}
		if n == 0 {
			if r != '-' && r != '*' && r != '_' {
				return false
			}
			c = r
		} else if r != c {
			return false
		}
		n++
	}
	return n >= 3
}

// MarkdownToTelegramHTML is markdownToTelegramHtml.
func MarkdownToTelegramHTML(md string) string {
	sv := &saver{}
	var parts []string
	for _, seg := range splitFences(md) {
		if seg.code {
			cls := ""
			if seg.lang != "" {
				cls = ` class="language-` + EscapeHTML(seg.lang) + `"`
			}
			parts = append(parts, "<pre><code"+cls+">"+EscapeHTML(seg.body)+"</code></pre>")
			continue
		}
		var out, quote []string
		flushQuote := func() {
			if len(quote) > 0 {
				out = append(out, "<blockquote>"+strings.Join(quote, "\n")+"</blockquote>")
			}
			quote = nil
		}
		for _, raw := range strings.Split(seg.body, "\n") {
			if q, ok := quoteLine(raw); ok {
				quote = append(quote, inlineToHTML(q, sv))
				continue
			}
			flushQuote()
			if h, ok := headingOf(raw); ok {
				out = append(out, "<b>"+inlineToHTML(h, sv)+"</b>")
				continue
			}
			if lead, rest, ok := bulletOf(raw, "-*+"); ok {
				out = append(out, lead+"• "+inlineToHTML(rest, sv))
				continue
			}
			if isRule(raw) {
				out = append(out, "──────")
				continue
			}
			out = append(out, inlineToHTML(raw, sv))
		}
		flushQuote()
		parts = append(parts, strings.Join(out, "\n"))
	}
	return sv.restore(strings.Join(parts, "\n"))
}

const bold = "\u0001"

func inlineToWhatsApp(line string, sv *saver) string {
	s := replaceInlineCode(line, func(m, _ string) string { return sv.ph(m) })
	s = replaceLinks(s, func(_, label, url string) string {
		if strx.Trim(label) == strx.Trim(url) {
			return url
		}
		return label + " (" + url + ")"
	})
	s = emph{marker: []rune("**"), excl: '*', open: bold, close: bold}.apply(s)
	s = emph{marker: []rune("__"), excl: '_', open: bold, close: bold}.apply(s)
	s = emph{marker: []rune("~~"), excl: '~', open: "~", close: "~"}.apply(s)
	s = emph{marker: []rune("*"), excl: '*', prefixed: true, preExcl: '*', afterExcl: '*', open: "_", close: "_"}.apply(s)
	return s
}

// MarkdownToWhatsApp is markdownToWhatsApp.
func MarkdownToWhatsApp(md string) string {
	sv := &saver{}
	var parts []string
	for _, seg := range splitFences(md) {
		if seg.code {
			parts = append(parts, "```\n"+seg.body+"\n```")
			continue
		}
		lines := strings.Split(seg.body, "\n")
		out := make([]string, len(lines))
		for i, raw := range lines {
			if h, ok := headingOf(raw); ok {
				out[i] = bold + strings.ReplaceAll(inlineToWhatsApp(h, sv), bold, "") + bold
			} else if lead, rest, ok := bulletOf(raw, "*+"); ok {
				out[i] = lead + "- " + inlineToWhatsApp(rest, sv)
			} else {
				out[i] = inlineToWhatsApp(raw, sv)
			}
		}
		parts = append(parts, strings.Join(out, "\n"))
	}
	return strings.ReplaceAll(sv.restore(strings.Join(parts, "\n")), bold, "*")
}
