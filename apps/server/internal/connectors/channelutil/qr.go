package channelutil

import (
	"strconv"
	"strings"

	qr "github.com/piglig/go-qr"
)

// QRMatrix encodes text as a QR code at error correction level M (the
// qrcode package's default) and returns its modules, row by row (true is
// dark), without a quiet zone.
func QRMatrix(text string) ([][]bool, error) {
	segs, err := qr.MakeSegments(text)
	if err != nil {
		return nil, err
	}
	code, err := qr.EncodeSegments(segs, qr.Medium, 1, 40, -1, false)
	if err != nil {
		return nil, err
	}
	n := code.Size()
	out := make([][]bool, n)
	for y := range n {
		out[y] = make([]bool, n)
		for x := range n {
			out[y][x] = code.Module(x, y)
		}
	}
	return out, nil
}

// QRSVG is QRCode.toString(text, { type: "svg", margin: 1 }): the same markup
// the qrcode package's svg-tag renderer writes (a white background path, one
// black stroke path of horizontal runs, a viewBox and crispEdges, no width),
// so it scales and themes the same way. The module matrix comes from go-qr;
// it encodes the same data at the same level, but the mode split, version or
// mask may differ from the qrcode package's choice.
func QRSVG(text string) (string, error) {
	m, err := QRMatrix(text)
	if err != nil {
		return "", err
	}
	return renderSVG(m, 1), nil
}

func renderSVG(m [][]bool, margin int) string {
	size := len(m)
	total := strconv.Itoa(size + margin*2)
	var path strings.Builder
	moveBy, lineLength := 0, 0
	newRow := false
	at := func(i int) bool { return m[i/size][i%size] }
	for i := 0; i < size*size; i++ {
		col, row := i%size, i/size
		if col == 0 && !newRow {
			newRow = true
		}
		if at(i) {
			lineLength++
			if !(i > 0 && col > 0 && at(i-1)) {
				if newRow {
					path.WriteString("M" + strconv.Itoa(col+margin) + " " + strconv.FormatFloat(0.5+float64(row+margin), 'f', -1, 64))
				} else {
					path.WriteString("m" + strconv.Itoa(moveBy) + " 0")
				}
				moveBy = 0
				newRow = false
			}
			if !(col+1 < size && at(i+1)) {
				path.WriteString("h" + strconv.Itoa(lineLength))
				lineLength = 0
			}
		} else {
			moveBy++
		}
	}
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ` + total + " " + total + `" shape-rendering="crispEdges">` +
		`<path fill="#ffffff" d="M0 0h` + total + "v" + total + `H0z"/>` +
		`<path stroke="#000000" d="` + path.String() + `"/></svg>` + "\n"
}
