package secret_test

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/secret"
)

// Goldens exported by testdata/gen/goldens.ts from the TS crypto and ids code.

func readGolden(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

type cryptoGolden struct {
	MasterKey     string `json:"masterKey"`
	DerivedKeyHex string `json:"derivedKeyHex"`
	ControlToken  string `json:"controlToken"`
	Text          []struct {
		Plaintext string `json:"plaintext"`
		Blob      string `json:"blob"`
	} `json:"text"`
	Binary []struct {
		PlainBase64 string `json:"plainBase64"`
		BlobBase64  string `json:"blobBase64"`
	} `json:"binary"`
	JSON []struct {
		Value       json.RawMessage `json:"value"`
		Stringified string          `json:"stringified"`
		Blob        string          `json:"blob"`
	} `json:"json"`
	Sha256 []struct {
		Input string `json:"input"`
		Hex   string `json:"hex"`
	} `json:"sha256"`
	ShortKeys []string `json:"shortKeys"`
}

func ivOf(t *testing.T, blob string) []byte {
	t.Helper()
	iv, err := base64.RawURLEncoding.DecodeString(strings.Split(blob, ".")[1])
	if err != nil {
		t.Fatal(err)
	}
	return iv
}

func TestGoldenCrypto(t *testing.T) {
	var g cryptoGolden
	readGolden(t, "crypto.json", &g)
	key, err := secret.DeriveKey(g.MasterKey)
	if err != nil || hex.EncodeToString(key) != g.DerivedKeyHex {
		t.Fatal("derived key differs")
	}
	if tok, _ := secret.ControlToken(g.MasterKey); tok != g.ControlToken {
		t.Fatal("control token differs")
	}
	for i, c := range g.Text {
		plain, err := secret.Decrypt(c.Blob, g.MasterKey)
		if err != nil || plain != c.Plaintext {
			t.Errorf("text %d: decrypt %v", i, err)
		}
		again, err := secret.EncryptWithIV(c.Plaintext, g.MasterKey, ivOf(t, c.Blob))
		if err != nil || again != c.Blob {
			t.Errorf("text %d: Go ciphertext differs", i)
		}
	}
	for i, c := range g.Binary {
		blob, _ := base64.StdEncoding.DecodeString(c.BlobBase64)
		want, _ := base64.StdEncoding.DecodeString(c.PlainBase64)
		plain, err := secret.DecryptBytes(blob, g.MasterKey)
		if err != nil || string(plain) != string(want) {
			t.Errorf("binary %d: decrypt %v", i, err)
		}
		again, err := secret.EncryptBytesWithIV(want, g.MasterKey, blob[:12])
		if err != nil || string(again) != string(blob) {
			t.Errorf("binary %d: Go ciphertext differs", i)
		}
	}
	for i, c := range g.JSON {
		m, err := secret.DecryptJSON(c.Blob, g.MasterKey)
		if err != nil || jsonx.Stringify(m) != c.Stringified {
			t.Errorf("json %d: %s %v", i, jsonx.Stringify(m), err)
		}
		// The Go side writes the same JSON text for the same ordered map.
		again, err := secret.EncryptWithIV(jsonx.Stringify(m), g.MasterKey, ivOf(t, c.Blob))
		if err != nil || again != c.Blob {
			t.Errorf("json %d: Go ciphertext differs", i)
		}
	}
	for _, c := range g.Sha256 {
		if secret.Sha256(c.Input) != c.Hex {
			t.Errorf("sha256(%q)", c.Input)
		}
	}
	for _, k := range g.ShortKeys {
		var mk secret.MasterKeyError
		if _, err := secret.Encrypt("x", k); !errors.As(err, &mk) {
			t.Errorf("key %q accepted", k)
		}
	}
}

func TestGoldenMask(t *testing.T) {
	var cases []struct {
		Input  string `json:"input"`
		Masked string `json:"masked"`
	}
	readGolden(t, "mask.json", &cases)
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range cases {
		if got := secret.MaskSecret(c.Input); got != c.Masked {
			t.Errorf("MaskSecret(%q) = %q, TS %q", c.Input, got, c.Masked)
		}
	}
}

func TestGoldenIDs(t *testing.T) {
	var scenarios []struct {
		Name     string   `json:"name"`
		Clock    []int64  `json:"clock"`
		Draws    []string `json:"draws"`
		Prefixes []string `json:"prefixes"`
		IDs      []string `json:"ids"`
	}
	readGolden(t, "ids.json", &scenarios)
	if len(scenarios) == 0 {
		t.Fatal("no scenarios")
	}
	for _, s := range scenarios {
		clock := append([]int64(nil), s.Clock...)
		draw := 0
		next := secret.NewIDGenerator(source{
			now: func() int64 {
				v := clock[0]
				clock = clock[1:]
				return v
			},
			draws: func() []int {
				d := digits(s.Draws[draw])
				draw++
				return d
			},
		})
		for i, p := range s.Prefixes {
			if got := next(p); got != s.IDs[i] {
				t.Errorf("%s #%d: %s, TS %s", s.Name, i, got, s.IDs[i])
			}
		}
	}
}

func TestGoldenTokens(t *testing.T) {
	var g struct {
		RandomToken []struct {
			Bytes  int    `json:"bytes"`
			Sample string `json:"sample"`
			Length int    `json:"length"`
		} `json:"randomToken"`
		HookSecret struct{ Sample, Pattern string } `json:"hookSecret"`
		HookSlug   struct{ Sample, Pattern string } `json:"hookSlug"`
		ShareToken struct{ Sample, Pattern string } `json:"shareToken"`
		ClaimNonce struct{ Sample, Pattern string } `json:"claimNonce"`
	}
	readGolden(t, "tokens.json", &g)
	charset := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	for _, c := range g.RandomToken {
		got := secret.RandomToken(c.Bytes)
		if len(got) != c.Length || len(c.Sample) != c.Length || !charset.MatchString(got) {
			t.Errorf("RandomToken(%d) = %q, TS length %d", c.Bytes, got, c.Length)
		}
	}
	for name, c := range map[string]struct {
		sample, pattern string
		ours            string
	}{
		"hook secret": {g.HookSecret.Sample, g.HookSecret.Pattern, "phk_" + secret.RandomToken(32)},
		"hook slug":   {g.HookSlug.Sample, g.HookSlug.Pattern, secret.RandomToken(12)},
		"share token": {g.ShareToken.Sample, g.ShareToken.Pattern, secret.RandomToken(24)},
		"claim nonce": {g.ClaimNonce.Sample, g.ClaimNonce.Pattern, secret.RandomToken(12)},
	} {
		re := regexp.MustCompile(c.pattern)
		if !re.MatchString(c.sample) || !re.MatchString(c.ours) {
			t.Errorf("%s: TS %q, Go %q, pattern %s", name, c.sample, c.ours, c.pattern)
		}
	}
}
