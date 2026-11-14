// Package secret ports packages/db/src/crypto.ts and ids.ts: secrets at rest
// (AES-256-GCM with a key derived from MASTER_KEY), masking, hashes, ids and
// random tokens. Formats are byte-compatible with the TS build.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// MasterKeyError is returned for a missing or short MASTER_KEY.
type MasterKeyError struct{}

func (MasterKeyError) Error() string {
	return "MASTER_KEY must be set and at least 32 characters (openssl rand -base64 32)."
}

// ErrUnknownFormat is returned for a blob that isn't "v1.<iv>.<tag>.<ct>".
var ErrUnknownFormat = errors.New("Unknown secret format")

// ErrUnknownBlob is returned for a binary blob shorter than iv plus tag.
var ErrUnknownBlob = errors.New("Unknown blob format")

var b64 = base64.RawURLEncoding

// DeriveKey is SHA-256("palui:secrets:v1:" + masterKey).
func DeriveKey(masterKey string) ([]byte, error) {
	if masterKey == "" || strx.Len16(masterKey) < 32 {
		return nil, MasterKeyError{}
	}
	sum := sha256.Sum256([]byte("palui:secrets:v1:" + masterKey))
	return sum[:], nil
}

func gcmFor(masterKey string) (cipher.AEAD, error) {
	key, err := DeriveKey(masterKey)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encrypt returns "v1.<iv>.<tag>.<ct>", each part base64url without padding.
func Encrypt(plaintext, masterKey string) (string, error) {
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	return encryptWithIV(plaintext, masterKey, iv)
}

func encryptWithIV(plaintext, masterKey string, iv []byte) (string, error) {
	g, err := gcmFor(masterKey)
	if err != nil {
		return "", err
	}
	sealed := g.Seal(nil, iv, []byte(plaintext), nil)
	ct, tag := sealed[:len(sealed)-16], sealed[len(sealed)-16:]
	return strings.Join([]string{"v1", b64.EncodeToString(iv), b64.EncodeToString(tag), b64.EncodeToString(ct)}, "."), nil
}

// decodeB64URL is Buffer.from(s, "base64url"): padding and stray characters
// are tolerated.
func decodeB64URL(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	return b64.DecodeString(s)
}

// Decrypt opens a text-form blob.
func Decrypt(blob, masterKey string) (string, error) {
	parts := strings.Split(blob, ".")
	if len(parts) < 4 || parts[0] != "v1" || parts[1] == "" || parts[2] == "" {
		return "", ErrUnknownFormat
	}
	g, err := gcmFor(masterKey)
	if err != nil {
		return "", err
	}
	iv, err := decodeB64URL(parts[1])
	if err != nil {
		return "", err
	}
	tag, err := decodeB64URL(parts[2])
	if err != nil {
		return "", err
	}
	ct, err := decodeB64URL(parts[3])
	if err != nil {
		return "", err
	}
	if len(iv) != 12 || len(tag) != 16 {
		return "", errors.New("Unsupported state or unable to authenticate data")
	}
	plain, err := g.Open(nil, iv, append(ct, tag...), nil)
	if err != nil {
		return "", errors.New("Unsupported state or unable to authenticate data")
	}
	return strx.DecodeUTF8(plain), nil
}

// EncryptBytes is the binary form for file bodies: iv (12) | tag (16) | ciphertext.
func EncryptBytes(plain []byte, masterKey string) ([]byte, error) {
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	return encryptBytesWithIV(plain, masterKey, iv)
}

func encryptBytesWithIV(plain []byte, masterKey string, iv []byte) ([]byte, error) {
	g, err := gcmFor(masterKey)
	if err != nil {
		return nil, err
	}
	sealed := g.Seal(nil, iv, plain, nil)
	ct, tag := sealed[:len(sealed)-16], sealed[len(sealed)-16:]
	out := make([]byte, 0, 28+len(ct))
	out = append(out, iv...)
	out = append(out, tag...)
	return append(out, ct...), nil
}

// DecryptBytes opens a binary-form blob.
func DecryptBytes(blob []byte, masterKey string) ([]byte, error) {
	if len(blob) < 28 {
		return nil, ErrUnknownBlob
	}
	g, err := gcmFor(masterKey)
	if err != nil {
		return nil, err
	}
	ct := append(append([]byte(nil), blob[28:]...), blob[12:28]...)
	plain, err := g.Open(nil, blob[:12], ct, nil)
	if err != nil {
		return nil, errors.New("Unsupported state or unable to authenticate data")
	}
	return plain, nil
}

// EncryptJSON is encrypt(JSON.stringify(value)). value is a string map:
// *jsonx.Map[string] keeps its key order, a Go map is written sorted.
func EncryptJSON(value any, masterKey string) (string, error) {
	b, err := jsonx.Marshal(value)
	if err != nil {
		return "", err
	}
	return Encrypt(string(b), masterKey)
}

// DecryptJSON opens a JSON object of strings, keeping its key order. An empty
// blob gives an empty map.
func DecryptJSON(blob string, masterKey string) (*jsonx.Map[string], error) {
	out := jsonx.NewMap[string]()
	if blob == "" {
		return out, nil
	}
	plain, err := Decrypt(blob, masterKey)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(plain), out); err != nil {
		return nil, err
	}
	return out, nil
}

// MaskTailMinLength: values this long or longer show their last 4 characters.
const MaskTailMinLength = 13

var maskPrefix = regexp.MustCompile(`^(rcl_|zkg_|sk-|pk_|ghp_)`)

// MaskSecret shows a known prefix plus the last 4 characters. Values of 12
// characters or fewer get no tail, so a short secret never reaches the browser whole.
func MaskSecret(value string) string {
	if value == "" {
		return ""
	}
	prefix := ""
	if m := maskPrefix.FindStringSubmatch(value); m != nil {
		prefix = m[1]
	}
	tail := ""
	if strx.Len16(value) >= MaskTailMinLength {
		tail = strx.Tail16(value, 4)
	}
	return prefix + "••••" + tail
}

// ControlToken is the old app to runtime control token (HMAC of "palui:control").
// The control API is gone in the Go build; kept for parity checks.
func ControlToken(masterKey string) (string, error) {
	key, err := DeriveKey(masterKey)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("palui:control"))
	return b64.EncodeToString(mac.Sum(nil)), nil
}

// Sha256 is the hex SHA-256 of the UTF-8 string.
func Sha256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// SafeEqual compares in constant time; different lengths are unequal.
func SafeEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
