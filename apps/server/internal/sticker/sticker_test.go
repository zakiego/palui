package sticker

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

func load(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func toImage(t *testing.T, name string) image.Image {
	t.Helper()
	out, err := ToPNG(load(t, name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if !bytes.HasPrefix(out, []byte("\x89PNG")) {
		t.Fatalf("%s: not a png", name)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func rgba(c color.Color) [4]uint32 {
	r, g, b, a := c.RGBA()
	return [4]uint32{r >> 8, g >> 8, b >> 8, a >> 8}
}

func near(got [4]uint32, want [4]uint32) bool {
	for i := range got {
		d := int(got[i]) - int(want[i])
		if d < -12 || d > 12 {
			return false
		}
	}
	return true
}

func TestStill(t *testing.T) {
	img := toImage(t, "still-lossless.webp")
	if b := img.Bounds(); b.Dx() != 24 || b.Dy() != 16 {
		t.Fatalf("bounds %v", b)
	}
	if got := rgba(img.At(3, 3)); !near(got, [4]uint32{255, 0, 0, 255}) {
		t.Errorf("pixel %v", got)
	}
	img = toImage(t, "still-lossy-alpha.webp")
	if got := rgba(img.At(3, 3)); got[3] < 100 || got[3] > 160 {
		t.Errorf("alpha lost: %v", got)
	}
}

func TestAnimatedIsFirstFrame(t *testing.T) {
	// Lossless frames (VP8L): red first, then blue.
	if !IsAnimated(load(t, "animated-lossless.webp")) {
		t.Fatal("not seen as animated")
	}
	img := toImage(t, "animated-lossless.webp")
	if b := img.Bounds(); b.Dx() != 24 || b.Dy() != 16 {
		t.Fatalf("bounds %v", b)
	}
	if got := rgba(img.At(5, 5)); !near(got, [4]uint32{255, 0, 0, 255}) {
		t.Errorf("pixel %v", got)
	}
	// Lossy frames with a separate ALPH chunk: half transparent blue first.
	img = toImage(t, "animated-lossy-alpha.webp")
	r, _, b, a := img.At(5, 5).RGBA()
	if a>>8 < 100 || a>>8 > 160 || b <= r {
		t.Errorf("lossy alpha frame: %v", rgba(img.At(5, 5)))
	}
}

func TestAnimatedFrameOffset(t *testing.T) {
	// An 8x8 green frame at (4,2) on a 24x16 canvas.
	img := toImage(t, "animated-offset.webp")
	if b := img.Bounds(); b.Dx() != 24 || b.Dy() != 16 {
		t.Fatalf("bounds %v", b)
	}
	if got := rgba(img.At(6, 5)); !near(got, [4]uint32{0, 255, 0, 255}) {
		t.Errorf("inside frame %v", got)
	}
	if got := rgba(img.At(1, 1)); got[3] != 0 {
		t.Errorf("outside frame should be transparent, got %v", got)
	}
}

func TestRejects(t *testing.T) {
	for name, b := range map[string][]byte{
		"empty":     nil,
		"not webp":  []byte("media:ST1"),
		"png":       []byte("\x89PNG\r\n\x1a\n0000000000"),
		"truncated": load(t, "animated-lossless.webp")[:40],
	} {
		if _, err := ToPNG(b); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if IsAnimated(load(t, "still-lossless.webp")) {
		t.Error("still seen as animated")
	}
}

func TestCanvasTooLarge(t *testing.T) {
	b := append([]byte(nil), load(t, "animated-lossless.webp")...)
	// VP8X payload starts at 20; canvas width-1 is at bytes 24..26.
	put24(b[24:27], MaxSide+10)
	if _, err := ToPNG(b); err == nil {
		t.Error("want an error for a huge canvas")
	}
}
