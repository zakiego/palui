package tools

// Tool search (PLAN 5.2), from tools/search.ts: when a turn has more tools than
// budgets.maxTools, only a core set is loaded and the rest is found by keyword
// through palui__search_tools. Pure ranking here; loading lives in collect.go.

import (
	"sort"
	"strings"
	"unicode"

	"github.com/zakiego/palui/apps/server/internal/strx"
)

type SearchDoc struct {
	// Namespaced tool name, <connectorId>__<tool>.
	Name           string
	ConnectorID    string
	ConnectorLabel string
	Tool           string
	Title          string
	Description    string
}

type SearchHit struct {
	Doc   SearchDoc
	Score int
}

// jsLower is String.prototype.toLowerCase for what Go's ToLower does
// differently: dotted capital I keeps its combining dot, and a final capital
// sigma becomes the final form.
func jsLower(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		switch {
		case r == 'İ':
			b.WriteString("i̇")
		case r == 'Σ':
			prevLetter := i > 0 && unicode.IsLetter(rs[i-1])
			nextLetter := i+1 < len(rs) && unicode.IsLetter(rs[i+1])
			if prevLetter && !nextLetter {
				b.WriteRune('ς')
			} else {
				b.WriteRune('σ')
			}
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// Tokens are lowercase words, split on anything that isn't a letter or digit,
// longer than one character (UTF-16 units), so get_prayer_times,
// jadwal-sholat and "Jadwal Sholat" tokenize alike.
func Tokens(s string) []string {
	out := []string{}
	for _, w := range strings.FieldsFunc(jsLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		if strx.Len16(w) > 1 {
			out = append(out, w)
		}
	}
	return out
}

// Field weights: a word in the tool or connector name says more than one in a
// long description.
var fields = []struct {
	weight int
	text   func(d SearchDoc) string
}{
	{4, func(d SearchDoc) string { return d.Tool }},
	{3, func(d SearchDoc) string { return d.ConnectorLabel + " " + d.ConnectorID }},
	{3, func(d SearchDoc) string { return d.Title }},
	{1, func(d SearchDoc) string { return d.Description }},
}

func scoreDoc(doc SearchDoc, query []string) int {
	score := 0
	for _, f := range fields {
		words := Tokens(f.text(doc))
		if len(words) == 0 {
			continue
		}
		for _, q := range query {
			if contains(words, q) {
				score += f.weight * 2
				continue
			}
			// Prefix either way: "sholat" finds "sholatnya", "prayer" finds "prayers".
			if strx.Len16(q) >= 3 {
				for _, w := range words {
					if strx.Len16(w) >= 3 && (strings.HasPrefix(w, q) || strings.HasPrefix(q, w)) {
						score += f.weight
						break
					}
				}
			}
		}
	}
	return score
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// SearchTools is searchTools: best matches first; ties keep the input order.
func SearchTools(docs []SearchDoc, query string, limit int) []SearchHit {
	var q []string
	seen := map[string]bool{}
	for _, t := range Tokens(query) {
		if !seen[t] {
			seen[t] = true
			q = append(q, t)
		}
	}
	if len(q) == 0 {
		return []SearchHit{}
	}
	type ranked struct {
		hit SearchHit
		i   int
	}
	var hits []ranked
	for i, d := range docs {
		if s := scoreDoc(d, q); s > 0 {
			hits = append(hits, ranked{SearchHit{d, s}, i})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool {
		if hits[a].hit.Score != hits[b].hit.Score {
			return hits[a].hit.Score > hits[b].hit.Score
		}
		return hits[a].i < hits[b].i
	})
	out := []SearchHit{}
	for i, h := range hits {
		if i >= limit {
			break
		}
		out = append(out, h.hit)
	}
	return out
}
