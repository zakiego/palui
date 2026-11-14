package secret

import (
	"crypto/rand"
	"strconv"
	"strings"
	"sync"
	"time"
)

const idAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// IDRandomLength is the number of base36 random digits in an id.
const IDRandomLength = 10

// IDSource feeds an id generator: the clock in Unix ms, and IDRandomLength
// base36 digits (0 to 35), most significant first.
type IDSource interface {
	Now() int64
	Random() []int
}

type systemSource struct{}

func (systemSource) Now() int64 { return time.Now().UnixMilli() }

// Random draws like the TS build: one random byte per digit, modulo 36.
func (systemSource) Random() []int {
	b := make([]byte, IDRandomLength)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	out := make([]int, IDRandomLength)
	for i, x := range b {
		out[i] = int(x) % len(idAlphabet)
	}
	return out
}

// SystemIDSource is the wall clock plus crypto/rand.
var SystemIDSource IDSource = systemSource{}

// increment adds one in place; false when every digit was already the highest.
func increment(digits []int) bool {
	for i := len(digits) - 1; i >= 0; i-- {
		if digits[i] < len(idAlphabet)-1 {
			digits[i]++
			return true
		}
		digits[i] = 0
	}
	return false
}

// NewIDGenerator returns a generator whose ids strictly increase in the order
// it made them. A new millisecond draws a fresh random part. Inside one
// millisecond, or when the clock steps back, the next id is the previous one
// plus one (ULID's monotonic mode). If the random part runs out, the id borrows
// the next millisecond. Safe for concurrent use.
func NewIDGenerator(src IDSource) func(prefix string) string {
	if src == nil {
		src = SystemIDSource
	}
	var mu sync.Mutex
	var last int64 = -1
	var digits []int
	return func(prefix string) string {
		mu.Lock()
		defer mu.Unlock()
		now := src.Now()
		if now > last {
			last = now
			digits = append([]int(nil), src.Random()...)
		} else if !increment(digits) {
			last++
			digits = append([]int(nil), src.Random()...)
		}
		var b strings.Builder
		b.Grow(len(prefix) + 20)
		b.WriteString(prefix)
		b.WriteByte('_')
		stamp := strconv.FormatInt(last, 36)
		for i := len(stamp); i < 9; i++ {
			b.WriteByte('0')
		}
		b.WriteString(stamp)
		for _, d := range digits {
			b.WriteByte(idAlphabet[d])
		}
		return b.String()
	}
}

// NewID makes time-ordered ids: "<prefix>_<ms base36, 9 chars><10 random chars>".
// Ids are names, not secrets. Anything that grants access uses RandomToken.
var NewID = NewIDGenerator(SystemIDSource)

// RandomToken is randomBytes(n) as base64url without padding (24 bytes by default in TS).
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b64.EncodeToString(b)
}
