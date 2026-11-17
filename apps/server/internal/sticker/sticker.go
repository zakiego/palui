// Package sticker turns a chat sticker (WebP, still or animated) into a PNG a
// vision model accepts. Animated stickers become their first frame.
package sticker

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/png"

	"golang.org/x/image/webp"
)

// MaxSide caps the canvas so a crafted header can't make us allocate
// gigabytes. Stickers are 512x512.
const MaxSide = 4096

var errNotWebP = errors.New("sticker: not a webp file")

type chunk struct {
	id   string
	data []byte
}

// chunks splits a RIFF WEBP file into its top-level chunks.
func chunks(b []byte) ([]chunk, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return nil, errNotWebP
	}
	return subChunks(b[12:])
}

func subChunks(b []byte) ([]chunk, error) {
	var out []chunk
	for len(b) >= 8 {
		size := int(binary.LittleEndian.Uint32(b[4:8]))
		if size < 0 || size > len(b)-8 {
			return nil, errors.New("sticker: truncated chunk")
		}
		out = append(out, chunk{id: string(b[0:4]), data: b[8 : 8+size]})
		next := 8 + size + size%2
		if next > len(b) {
			break
		}
		b = b[next:]
	}
	return out, nil
}

func u24(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }

func put24(b []byte, v int) { b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16) }

func writeChunk(buf *bytes.Buffer, id string, data []byte) {
	buf.WriteString(id)
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(len(data)))
	buf.Write(n[:])
	buf.Write(data)
	if len(data)%2 == 1 {
		buf.WriteByte(0)
	}
}

func riff(body []byte) []byte {
	var out bytes.Buffer
	out.WriteString("RIFF")
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(4+len(body)))
	out.Write(n[:])
	out.WriteString("WEBP")
	out.Write(body)
	return out.Bytes()
}

// IsAnimated reports whether a WebP file has the animation flag set.
func IsAnimated(b []byte) bool {
	cs, err := chunks(b)
	if err != nil || len(cs) == 0 || cs[0].id != "VP8X" || len(cs[0].data) < 10 {
		return false
	}
	return cs[0].data[0]&0x02 != 0
}

// firstFrame rebuilds the first ANMF frame of an animated WebP as a still
// WebP, and returns where it sits on the canvas.
func firstFrame(cs []chunk) (still []byte, canvas image.Rectangle, at image.Point, err error) {
	vp8x := cs[0].data
	cw, ch := u24(vp8x[4:7])+1, u24(vp8x[7:10])+1
	if cw > MaxSide || ch > MaxSide {
		return nil, image.Rectangle{}, image.Point{}, errors.New("sticker: canvas too large")
	}
	canvas = image.Rect(0, 0, cw, ch)
	for _, c := range cs {
		if c.id != "ANMF" || len(c.data) < 16 {
			continue
		}
		at = image.Pt(u24(c.data[0:3])*2, u24(c.data[3:6])*2)
		fw, fh := u24(c.data[6:9])+1, u24(c.data[9:12])+1
		parts, err := subChunks(c.data[16:])
		if err != nil {
			return nil, canvas, at, err
		}
		var alph, bits *chunk
		for i := range parts {
			switch parts[i].id {
			case "ALPH":
				alph = &parts[i]
			case "VP8 ", "VP8L":
				bits = &parts[i]
			}
		}
		if bits == nil {
			return nil, canvas, at, errors.New("sticker: frame has no image data")
		}
		var body bytes.Buffer
		if alph != nil && bits.id == "VP8 " {
			// Lossy with a separate alpha plane needs a VP8X header.
			hdr := make([]byte, 10)
			hdr[0] = 0x10
			put24(hdr[4:7], fw-1)
			put24(hdr[7:10], fh-1)
			writeChunk(&body, "VP8X", hdr)
			writeChunk(&body, "ALPH", alph.data)
		}
		writeChunk(&body, bits.id, bits.data)
		return riff(body.Bytes()), canvas, at, nil
	}
	return nil, canvas, at, errors.New("sticker: no frames")
}

// ToPNG converts a WebP sticker to PNG. An animated one becomes its first
// frame, drawn on a transparent canvas of the animation's size.
func ToPNG(b []byte) ([]byte, error) {
	cs, err := chunks(b)
	if err != nil {
		return nil, err
	}
	var img image.Image
	if IsAnimated(b) {
		still, canvas, at, err := firstFrame(cs)
		if err != nil {
			return nil, err
		}
		frame, err := decode(still)
		if err != nil {
			return nil, err
		}
		dst := image.NewNRGBA(canvas)
		draw.Draw(dst, frame.Bounds().Add(at), frame, frame.Bounds().Min, draw.Src)
		img = dst
	} else {
		if img, err = decode(b); err != nil {
			return nil, err
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func decode(b []byte) (image.Image, error) {
	cfg, err := webp.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	if cfg.Width > MaxSide || cfg.Height > MaxSide {
		return nil, errors.New("sticker: image too large")
	}
	return webp.Decode(bytes.NewReader(b))
}
