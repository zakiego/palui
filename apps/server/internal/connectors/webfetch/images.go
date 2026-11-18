package webfetch

// Image links on a page, so the agent can send a picture it found on the web
// (send_media takes a direct file link, and readable text has none). The
// page's own preview image (og:image, twitter:image) comes first: it's the
// picture the page picked to stand for itself. Then the page's <img> tags in
// order, without icons, tracking pixels, inline data and SVG.

import (
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/zakiego/palui/apps/server/internal/strx"
)

// MaxPageImages caps the image links one page hands the model.
const MaxPageImages = 8

// PageImage is one picture on a page: an absolute http(s) URL and its alt text.
type PageImage struct {
	URL string `json:"url"`
	Alt string `json:"alt,omitempty"`
}

// smallImage: width or height set under this is an icon, a badge or a pixel.
const smallImage = 100

var skipImageWords = []string{"logo", "icon", "avatar", "sprite", "pixel", "badge", "emoji", "spacer.gif", "blank.gif", "favicon"}

func imgAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return strx.Trim(a.Val)
		}
	}
	return ""
}

func small(n *html.Node) bool {
	for _, k := range []string{"width", "height"} {
		if v, err := strconv.Atoi(strings.TrimSuffix(imgAttr(n, k), "px")); err == nil && v > 0 && v < smallImage {
			return true
		}
	}
	return false
}

// firstSrc is the image URL of an <img>: src, then the lazy-load attributes
// pages use, then the first srcset candidate.
func firstSrc(n *html.Node) string {
	for _, k := range []string{"src", "data-src", "data-original", "data-lazy-src"} {
		if v := imgAttr(n, k); v != "" && !strings.HasPrefix(v, "data:") {
			return v
		}
	}
	if set := imgAttr(n, "srcset"); set != "" {
		return strings.Fields(strings.Split(set, ",")[0] + " ")[0]
	}
	return ""
}

// PageImages lists the pictures on an HTML page, preview image first, at most
// MaxPageImages, each once.
func PageImages(page, pageURL string) []PageImage {
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return nil
	}
	base, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}
	if b := find(doc, "base"); b != nil {
		if href := imgAttr(b, "href"); href != "" {
			if u, err := base.Parse(href); err == nil {
				base = u
			}
		}
	}
	var preview, inline []PageImage
	seen := map[string]bool{}
	add := func(list *[]PageImage, raw, alt string) {
		if raw == "" {
			return
		}
		u, err := base.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return
		}
		u.Fragment = ""
		abs := u.String()
		lower := strings.ToLower(u.Path)
		if seen[abs] || strings.HasSuffix(lower, ".svg") || strings.HasSuffix(lower, ".ico") || len(abs) > 2000 {
			return
		}
		seen[abs] = true
		*list = append(*list, PageImage{URL: abs, Alt: strx.Slice16(strx.CollapseSpace(alt), 200)})
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "meta":
				key := strings.ToLower(imgAttr(n, "property"))
				if key == "" {
					key = strings.ToLower(imgAttr(n, "name"))
				}
				if key == "og:image" || key == "og:image:url" || key == "og:image:secure_url" || key == "twitter:image" || key == "twitter:image:src" {
					add(&preview, imgAttr(n, "content"), "")
				}
			case "script", "style", "noscript", "template", "svg":
				return
			case "img":
				src := firstSrc(n)
				lower := strings.ToLower(src + " " + imgAttr(n, "class") + " " + imgAttr(n, "id"))
				skip := small(n) || imgAttr(n, "aria-hidden") == "true"
				for _, w := range skipImageWords {
					skip = skip || strings.Contains(lower, w)
				}
				if !skip {
					add(&inline, src, imgAttr(n, "alt"))
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	out := append(preview, inline...)
	if len(out) > MaxPageImages {
		out = out[:MaxPageImages]
	}
	return out
}
