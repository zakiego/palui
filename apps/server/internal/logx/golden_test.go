package logx_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/logx"
)

// Redaction cases exported by testdata/gen/goldens.ts from the TS redact().
func TestGoldenRedact(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "redact.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input    string `json:"input"`
		Redacted string `json:"redacted"`
	}
	if err := json.Unmarshal(b, &cases); err != nil || len(cases) == 0 {
		t.Fatal(err)
	}
	for i, c := range cases {
		if got := logx.Redact(c.Input); got != c.Redacted {
			t.Errorf("case %d: Go %q, TS %q", i, got, c.Redacted)
		}
	}
}
