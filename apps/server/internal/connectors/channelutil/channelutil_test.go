package channelutil_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors/channelutil"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

func eq[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func eqs(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") || len(got) != len(want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

var ws = regexp.MustCompile(`[` + strx.SpaceClass + `]+`)

func TestSplitMessage(t *testing.T) {
	split := channelutil.SplitMessage
	t.Run("returns short text as one chunk", func(t *testing.T) {
		eqs(t, split("halo", 100), []string{"halo"})
	})
	t.Run("prefers paragraph breaks", func(t *testing.T) {
		a, b := strings.Repeat("a", 60), strings.Repeat("b", 60)
		eqs(t, split(a+"\n\n"+b, 100), []string{a, b})
	})
	t.Run("falls back to lines, then sentences, then words", func(t *testing.T) {
		line := strings.Repeat("x", 50) + "\n" + strings.Repeat("y", 70)
		eqs(t, split(line, 100), []string{strings.Repeat("x", 50), strings.Repeat("y", 70)})
		sentences := strings.Repeat("Kalimat satu panjang sekali. ", 3) + strings.Repeat("z", 40)
		if c := split(sentences, 100); !strings.HasSuffix(c[0], ".") {
			t.Fatalf("first chunk %q", c[0])
		}
		var words []string
		for i := range 40 {
			words = append(words, fmt.Sprintf("kata%d", i))
		}
		for _, c := range split(strings.Join(words, " "), 50) {
			if strx.Len16(c) > 50 || strx.Trim(c) != c {
				t.Fatalf("chunk %q", c)
			}
		}
	})
	t.Run("hard cuts a single long word", func(t *testing.T) {
		chunks := split(strings.Repeat("w", 250), 100)
		eq(t, strings.Join(chunks, ""), strings.Repeat("w", 250))
		for _, c := range chunks {
			if len(c) > 100 {
				t.Fatal(c)
			}
		}
	})
	t.Run("never exceeds the limit and keeps all words", func(t *testing.T) {
		var b strings.Builder
		for i := range 300 {
			if i%17 == 0 {
				fmt.Fprintf(&b, "kalimat%d.\n\n", i)
			} else {
				fmt.Fprintf(&b, "kata%d ", i)
			}
		}
		text := b.String()
		chunks := split(text, 200)
		for _, c := range chunks {
			if strx.Len16(c) > 200 {
				t.Fatal(c)
			}
		}
		norm := func(s string) string { return strx.Trim(ws.ReplaceAllString(s, " ")) }
		eq(t, norm(strings.Join(chunks, " ")), norm(text))
	})
	t.Run("moves the cut before a code fence when possible", func(t *testing.T) {
		intro := strx.Trim(strings.Repeat("Intro text that is long enough to stand alone. ", 2))
		code := "```ts\n" + strings.Repeat("const x = 1\n", 6) + "```"
		chunks := split(intro+"\n"+code, 120)
		eq(t, chunks[0], intro)
		if !strings.HasPrefix(chunks[1], "```ts") {
			t.Fatal(chunks[1])
		}
	})
	t.Run("closes and reopens a fence that is bigger than the limit", func(t *testing.T) {
		var lines []string
		for i := range 30 {
			lines = append(lines, fmt.Sprintf("print(%d)", i))
		}
		chunks := split("```py\n"+strings.Join(lines, "\n")+"\n```", 100)
		if len(chunks) < 2 {
			t.Fatal(chunks)
		}
		for _, c := range chunks {
			if len(c) > 100 || !strings.HasPrefix(c, "```py") || !strings.HasSuffix(strings.TrimRight(c, " \n"), "```") {
				t.Fatalf("chunk %q", c)
			}
		}
	})
	t.Run("terminates when a long line starting with a fence is the opener", func(t *testing.T) {
		// Regression: the whole line used to be re-prefixed on every chunk (OOM).
		text := strings.Repeat("intro ", 50) + "\n```" + strings.Repeat("kata ", 300) + "\n" + strings.Repeat("isi baris\n", 400)
		chunks := split(text, 4096)
		if len(chunks) < 2 {
			t.Fatal(len(chunks))
		}
		for i, c := range chunks {
			if strx.Len16(c) > 4096 {
				t.Fatal(len(c))
			}
			if i > 0 && !(strings.HasPrefix(c, "```kata\n") || !strings.HasPrefix(c, "```")) {
				t.Fatalf("chunk %d starts %q", i, c[:20])
			}
		}
	})
	t.Run("terminates on random long inputs and keeps every chunk within the limit", func(t *testing.T) {
		seed := int64(42)
		rand := func() float64 {
			seed = (seed*1103515245 + 12345) % 2147483648
			return float64(seed) / 2147483648
		}
		pieces := []string{"```", "```ts", "```" + strings.Repeat("x", 40), "\n", "\n\n", " ", ". ", "word", strings.Repeat("y", 300), strings.Repeat("kata ", 80)}
		for round := range 200 {
			limit := []int{16, 50, 100, 4096}[round%4]
			text := ""
			target := limit * (2 + int(rand()*6))
			for strx.Len16(text) < target {
				text += pieces[int(rand()*float64(len(pieces)))]
			}
			chunks := split(text, limit)
			if len(chunks) >= strx.Len16(text)+2 {
				t.Fatal("too many chunks")
			}
			for _, c := range chunks {
				if strx.Len16(c) > limit {
					t.Fatalf("round %d: chunk of %d > %d", round, strx.Len16(c), limit)
				}
			}
		}
	})
	t.Run("refuses a limit under 16", func(t *testing.T) {
		defer func() {
			if recover() != channelutil.ErrLimitTooSmall {
				t.Fatal("no panic")
			}
		}()
		split("x", 15)
	})
}

func TestMarkdownToTelegramHTML(t *testing.T) {
	h := channelutil.MarkdownToTelegramHTML
	t.Run("escapes html special characters", func(t *testing.T) {
		eq(t, h("a < b && c > d"), "a &lt; b &amp;&amp; c &gt; d")
		eq(t, h("<script>alert(1)</script>"), "&lt;script&gt;alert(1)&lt;/script&gt;")
	})
	t.Run("converts bold, italic, strike and inline code", func(t *testing.T) {
		eq(t, h("**tebal** dan *miring* dan _miring_ ~~coret~~ `kode <x>`"), "<b>tebal</b> dan <i>miring</i> dan <i>miring</i> <s>coret</s> <code>kode &lt;x&gt;</code>")
	})
	t.Run("leaves snake_case and markers inside code alone", func(t *testing.T) {
		eq(t, h("pakai snake_case_name dan `**bukan**`"), "pakai snake_case_name dan <code>**bukan**</code>")
	})
	t.Run("renders code blocks as pre", func(t *testing.T) {
		eq(t, h("lihat:\n```ts\nif (a < b) **x**\n```\nselesai"), "lihat:\n<pre><code class=\"language-ts\">if (a &lt; b) **x**</code></pre>\nselesai")
	})
	t.Run("renders links and drops unsafe schemes", func(t *testing.T) {
		eq(t, h("[Palui](https://palui.zakiego.com/?a=1&b=2)"), `<a href="https://palui.zakiego.com/?a=1&amp;b=2">Palui</a>`)
		eq(t, h("[x](javascript:alert(1))"), "[x](javascript:alert(1))")
	})
	t.Run("renders headings, bullets and quotes", func(t *testing.T) {
		eq(t, h("# Judul\n- satu\n* dua\n> kutip"), "<b>Judul</b>\n• satu\n• dua\n<blockquote>kutip</blockquote>")
	})
}

func TestMarkdownToWhatsApp(t *testing.T) {
	w := channelutil.MarkdownToWhatsApp
	t.Run("converts bold, italic, strike and headings", func(t *testing.T) {
		eq(t, w("**tebal** *miring* _miring_ ~~coret~~"), "*tebal* _miring_ _miring_ ~coret~")
		eq(t, w("## Judul"), "*Judul*")
	})
	t.Run("keeps code untouched and turns links into text", func(t *testing.T) {
		eq(t, w("```\na **b**\n```"), "```\na **b**\n```")
		eq(t, w("`**x**` lihat [docs](https://x.id)"), "`**x**` lihat docs (https://x.id)")
		eq(t, w("[https://x.id](https://x.id)"), "https://x.id")
	})
	t.Run("turns star bullets into dashes", func(t *testing.T) {
		eq(t, w("* satu\n- dua"), "- satu\n- dua")
	})
}

func TestActionsHint(t *testing.T) {
	reminder := []db.OutboxAction{{ID: "rem:done:r1", Label: "Done"}, {ID: "rem:snooze:r1:1h", Label: "Tunda 1 jam"}, {ID: "rem:snooze:r1:tomorrow", Label: "Besok"}}
	t.Run("builds reply hints from action ids", func(t *testing.T) {
		eq(t, channelutil.ActionsHint(reminder), "Balas: done / tunda 1j / tunda besok")
		eq(t, channelutil.ActionsHint([]db.OutboxAction{{ID: "approve:a1", Label: "Setuju"}, {ID: "deny:a1", Label: "Tolak"}}), "Balas: ya / batal")
		eq(t, channelutil.ActionsHint([]db.OutboxAction{{ID: "custom", Label: "Lanjut"}}), "Balas: lanjut")
		eq(t, channelutil.ActionsHint([]db.OutboxAction{}), "")
		eq(t, channelutil.ActionsHint(nil), "")
	})
	t.Run("matches a typed reply back to its action", func(t *testing.T) {
		a, ok := channelutil.MatchActionReply(" Tunda Besok ", reminder)
		eq(t, ok, true)
		eq(t, a.ID, "rem:snooze:r1:tomorrow")
		_, ok = channelutil.MatchActionReply("nanti", reminder)
		eq(t, ok, false)
	})
}

func TestQRSVG(t *testing.T) {
	svg, err := channelutil.QRSVG("https://t.me/palui_test_bot?start=abc")
	if err != nil {
		t.Fatal(err)
	}
	m, _ := channelutil.QRMatrix("https://t.me/palui_test_bot?start=abc")
	size := fmt.Sprint(len(m) + 2)
	for _, want := range []string{`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ` + size + " " + size + `" shape-rendering="crispEdges">`, `<path fill="#ffffff" d="M0 0h` + size + "v" + size + `H0z"/>`, `<path stroke="#000000" d="M1 1.5h7`, "</svg>\n"} {
		if !strings.Contains(svg, want) {
			t.Fatalf("svg lacks %q:\n%s", want, svg)
		}
	}
}
