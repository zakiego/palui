package webfetch_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/webfetch"
)

const imagePage = `<!doctype html><html><head><title>Inhaler</title>
<meta property="og:image" content="/img/og-inhaler.jpg">
<meta name="twitter:image" content="https://cdn.example.com/img/og-inhaler.jpg">
</head><body>
<img src="/static/logo.png" alt="Logo">
<img src="data:image/gif;base64,R0lGOD" alt="pixel">
<img src="/img/tiny.png" width="16" height="16">
<img src="/img/icon.svg" alt="icon">
<img src="https://cdn.example.com/img/inhaler-spacer.jpg#zoom" alt="  Inhaler   dengan spacer ">
<img data-src="/img/lazy.jpg" alt="lazy">
<img srcset="/img/small.jpg 480w, /img/big.jpg 1080w" alt="srcset">
<img src="javascript:alert(1)">
<img src="https://cdn.example.com/img/inhaler-spacer.jpg" alt="duplicate">
<script>document.write('<img src="/img/script.jpg">')</script>
</body></html>`

func TestPageImagesPutsThePreviewFirstAndSkipsNoise(t *testing.T) {
	got := webfetch.PageImages(imagePage, "https://example.com/artikel/inhaler")
	want := []webfetch.PageImage{
		{URL: "https://example.com/img/og-inhaler.jpg"},
		{URL: "https://cdn.example.com/img/og-inhaler.jpg"},
		{URL: "https://cdn.example.com/img/inhaler-spacer.jpg", Alt: "Inhaler dengan spacer"},
		{URL: "https://example.com/img/lazy.jpg", Alt: "lazy"},
		{URL: "https://example.com/img/small.jpg", Alt: "srcset"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	var many strings.Builder
	for i := range 20 {
		fmt.Fprintf(&many, `<img src="/p/%d.jpg">`, i)
	}
	if n := len(webfetch.PageImages(many.String(), "https://example.com/")); n != webfetch.MaxPageImages {
		t.Fatal(n)
	}
	if got := webfetch.PageImages(`<base href="https://other.example.com/x/"><img src="a.jpg">`, "https://example.com/"); len(got) != 1 || got[0].URL != "https://other.example.com/x/a.jpg" {
		t.Fatalf("%+v", got)
	}
}

func TestFetchPageListsImagesOnlyWhenThereAreSome(t *testing.T) {
	out := conntest.Of(webfetch.FetchPage(context.Background(), "https://example.com/post", fake(response(imagePage, "text/html", 0, false), nil)))
	if out.Len("images") != 5 || out.S("images.0.url") != "https://example.com/img/og-inhaler.jpg" {
		t.Fatal(out)
	}
	if out.Has("images.0.alt") || out.S("images.2.alt") != "Inhaler dengan spacer" {
		t.Fatal(out)
	}
	plain := conntest.Of(webfetch.FetchPage(context.Background(), "https://example.com/post", fake(response(article, "text/html", 0, false), nil)))
	if plain.Has("images") {
		t.Fatal(plain)
	}
}
