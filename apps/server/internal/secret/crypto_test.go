package secret_test

import (
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/secret"
)

// Ports packages/db/src/db.test.ts "crypto" and ids.test.ts.

const key = "test-master-key-test-master-key-1234"

func TestEncryptRoundTripsWithFreshIV(t *testing.T) {
	a, err := secret.Encrypt("hello", key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := secret.Encrypt("hello", key)
	if a == b {
		t.Fatal("same ciphertext twice")
	}
	if got, err := secret.Decrypt(a, key); err != nil || got != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestDecryptFailsWithWrongKeyOrTamperedBlob(t *testing.T) {
	blob, _ := secret.Encrypt("secret", key)
	if _, err := secret.Decrypt(blob, key+"x"); err == nil {
		t.Fatal("wrong key decrypted")
	}
	parts := strings.Split(blob, ".")
	parts[3] = base64.RawURLEncoding.EncodeToString([]byte("tampered"))
	if _, err := secret.Decrypt(strings.Join(parts, "."), key); err == nil {
		t.Fatal("tampered blob decrypted")
	}
}

func TestRejectsShortMasterKey(t *testing.T) {
	_, err := secret.Encrypt("x", "short")
	var mk secret.MasterKeyError
	if !errors.As(err, &mk) {
		t.Fatalf("got %v", err)
	}
}

func TestEncryptsJSONAndMasks(t *testing.T) {
	m := jsonx.NewMap[string]()
	m.Set("apiKey", "rcl_abcdef123456a91f")
	blob, err := secret.EncryptJSON(m, key)
	if err != nil {
		t.Fatal(err)
	}
	back, err := secret.DecryptJSON(blob, key)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := back.Get("apiKey"); v != "rcl_abcdef123456a91f" || back.Len() != 1 {
		t.Fatalf("got %v", jsonx.Stringify(back))
	}
	empty, err := secret.DecryptJSON("", key)
	if err != nil || empty.Len() != 0 {
		t.Fatal("empty blob should be {}")
	}
	if got := secret.MaskSecret("rcl_abcdef123456a91f"); got != "rcl_••••a91f" {
		t.Fatal(got)
	}
	if got := secret.MaskSecret("1234567:abcdefgh"); got != "••••efgh" {
		t.Fatal(got)
	}
}

func TestMaskShowsNoTailForShortSecrets(t *testing.T) {
	cases := map[string]string{"": "", "abcd": "••••", "1234:abcd": "••••", "abcdefghijkl": "••••", "abcdefghijklm": "••••jklm", "sk-abc": "sk-••••"}
	for in, want := range cases {
		if got := secret.MaskSecret(in); got != want {
			t.Errorf("MaskSecret(%q) = %q, want %q", in, got, want)
		}
	}
	for _, short := range []string{"a", "ab", "abc", "abcd", "abcdefghijkl"} {
		if strings.Contains(secret.MaskSecret(short), short[len(short)-1:]) {
			t.Errorf("%q leaks its tail", short)
		}
	}
}

func TestBinaryForm(t *testing.T) {
	blob, err := secret.EncryptBytes([]byte{1, 2, 3}, key)
	if err != nil || len(blob) != 28+3 {
		t.Fatalf("len %d, %v", len(blob), err)
	}
	plain, err := secret.DecryptBytes(blob, key)
	if err != nil || string(plain) != "\x01\x02\x03" {
		t.Fatal(plain, err)
	}
	if _, err := secret.DecryptBytes(blob[:27], key); !errors.Is(err, secret.ErrUnknownBlob) {
		t.Fatal(err)
	}
}

func TestShaAndSafeEqual(t *testing.T) {
	if secret.Sha256("abc") != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatal("sha256")
	}
	if !secret.SafeEqual("abc", "abc") || secret.SafeEqual("abc", "abd") || secret.SafeEqual("abc", "abcd") {
		t.Fatal("SafeEqual")
	}
}

var idFormat = regexp.MustCompile(`^msg_[0-9a-z]{19}$`)

func TestNewIDStrictlyIncreasesInATightLoop(t *testing.T) {
	ids := make([]string, 5000)
	for i := range ids {
		ids[i] = secret.NewID("msg")
	}
	for i := 1; i < len(ids); i++ {
		if !(ids[i-1] < ids[i]) {
			t.Fatalf("id %d (%s) does not sort after id %d (%s)", i, ids[i], i-1, ids[i-1])
		}
	}
	for _, id := range ids {
		if !idFormat.MatchString(id) {
			t.Fatal(id)
		}
	}
}

func TestNewIDSortsAcrossMilliseconds(t *testing.T) {
	a := secret.NewID("msg")
	time.Sleep(2 * time.Millisecond)
	b := secret.NewID("msg")
	if !(a < b) || !idFormat.MatchString(a) {
		t.Fatal(a, b)
	}
}

type source struct {
	now   func() int64
	draws func() []int
}

func (s source) Now() int64    { return s.now() }
func (s source) Random() []int { return s.draws() }

var T = time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC).UnixMilli()

func stamp(ms int64) string {
	s := strings.ToLower(big36(ms))
	return strings.Repeat("0", 9-len(s)) + s
}

func big36(n int64) string {
	const a = "0123456789abcdefghijklmnopqrstuvwxyz"
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{a[n%36]}, out...)
		n /= 36
	}
	return string(out)
}

func digits(text string) []int {
	out := make([]int, len(text))
	for i, c := range text {
		out[i] = strings.IndexRune("0123456789abcdefghijklmnopqrstuvwxyz", c)
	}
	return out
}

func queue(draws ...[]int) func() []int {
	return func() []int {
		d := draws[0]
		draws = draws[1:]
		return d
	}
}

func fixed(ms int64) func() int64 { return func() int64 { return ms } }

func TestGeneratorDrawsFreshRandomWhenTheMillisecondMoves(t *testing.T) {
	clock := T
	next := secret.NewIDGenerator(source{func() int64 { return clock }, queue(digits("aaaaaaaaaa"), digits("0000000000"))})
	if got := next("msg"); got != "msg_"+stamp(T)+"aaaaaaaaaa" {
		t.Fatal(got)
	}
	clock = T + 1
	if got := next("msg"); got != "msg_"+stamp(T+1)+"0000000000" {
		t.Fatal(got)
	}
}

func TestGeneratorCountsUpWithinOneMillisecond(t *testing.T) {
	next := secret.NewIDGenerator(source{fixed(T), func() []int { return digits("000000000y") }})
	for _, want := range []string{"000000000y", "000000000z", "0000000010"} {
		if got := next("msg"); got != "msg_"+stamp(T)+want {
			t.Fatal(got)
		}
	}
}

func TestGeneratorCarriesAcrossSeveralPlaces(t *testing.T) {
	next := secret.NewIDGenerator(source{fixed(T), func() []int { return digits("a9zzzzzzzz") }})
	next("msg")
	if got := next("msg"); got != "msg_"+stamp(T)+"aa00000000" {
		t.Fatal(got)
	}
}

func TestGeneratorKeepsCountingWhenTheClockStepsBack(t *testing.T) {
	clock := T
	next := secret.NewIDGenerator(source{func() int64 { return clock }, func() []int { return digits("5555555555") }})
	a := next("msg")
	clock = T - 60_000
	b := next("msg")
	if b != "msg_"+stamp(T)+"5555555556" || !(a < b) {
		t.Fatal(a, b)
	}
}

func TestGeneratorBorrowsTheNextMillisecondOnOverflow(t *testing.T) {
	next := secret.NewIDGenerator(source{fixed(T), queue(digits("zzzzzzzzzz"), digits("3333333333"))})
	a, b, c := next("msg"), next("msg"), next("msg")
	if a != "msg_"+stamp(T)+"zzzzzzzzzz" || b != "msg_"+stamp(T+1)+"3333333333" || c != "msg_"+stamp(T+1)+"3333333334" {
		t.Fatal(a, b, c)
	}
	if !(a < b && b < c) {
		t.Fatal("order")
	}
}

func TestGeneratorOrdersAcrossPrefixes(t *testing.T) {
	next := secret.NewIDGenerator(source{fixed(T), func() []int { return digits("0000000000") }})
	a := next("msg")[4:]
	b := next("run")[4:]
	if !(a < b) {
		t.Fatal(a, b)
	}
}

func TestRandomToken(t *testing.T) {
	tok := secret.RandomToken(24)
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`).MatchString(tok) {
		t.Fatal(tok)
	}
	if len(secret.RandomToken(12)) != 16 {
		t.Fatal("12 bytes should be 16 characters")
	}
}
