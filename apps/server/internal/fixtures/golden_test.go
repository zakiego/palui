package fixtures_test

// The fixture goldens: each Go fixture applied to a fresh database must write
// exactly the rows the TS fixture writes (testdata/fixtures-<name>.json, made
// by testdata/gen/fixtures.ts). The export and its normalization of volatile
// values mirror the generator; see its header for the list.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/fixtures"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// goldenNow is the generator's NOW: 2026-11-14 10:00 WITA.
var goldenNow = time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC).UnixMilli()

func addOwner(t testing.TB, d *db.DB, createdAt int64) {
	t.Helper()
	_, err := d.Exec("INSERT INTO user (id, name, email, username, created_at, updated_at) VALUES ('user_owner', 'Owner', 'owner@palui.invalid', 'owner', ?, ?)", createdAt, createdAt)
	testutil.Must(t, err)
	_, err = d.Exec("INSERT INTO passkey (id, name, public_key, user_id, credential_id, counter, device_type, backed_up) VALUES ('pk_real', 'Real key', 'x', 'user_owner', 'cred_real', 0, 'multiDevice', 1)")
	testutil.Must(t, err)
}

var (
	idRE = regexp.MustCompile(`[a-z]+_0[0-9a-z]{18}[0-9a-z]*`)
	fxRE = regexp.MustCompile(`fx_[0-9a-z]{1,19}`)
)

func shaHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

type normalizer struct {
	seen   map[string]string
	counts map[string]int
}

func (n *normalizer) str(s string) string {
	return idRE.ReplaceAllStringFunc(s, func(m string) string {
		us := strings.IndexByte(m, '_')
		if len(m)-us-1 != 19 {
			return m
		}
		if p, ok := n.seen[m]; ok {
			return p
		}
		prefix := m[:us]
		n.counts[prefix]++
		p := fmt.Sprintf("<%s#%d>", prefix, n.counts[prefix])
		n.seen[m] = p
		return p
	})
}

type exporter struct {
	t   testing.TB
	d   *db.DB
	key string
	n   *normalizer
}

func (e *exporter) rows(query string) ([]string, []map[string]any) {
	rows, err := e.d.Query(query)
	testutil.Must(e.t, err)
	defer rows.Close()
	cols, err := rows.Columns()
	testutil.Must(e.t, err)
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		testutil.Must(e.t, rows.Scan(ptrs...))
		m := map[string]any{}
		for i, c := range cols {
			m[c] = vals[i]
		}
		out = append(out, m)
	}
	testutil.Must(e.t, rows.Err())
	return cols, out
}

func asString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case []byte:
		return string(x), true
	}
	return "", false
}

func (e *exporter) rowValues(table string, cols []string, row map[string]any) []any {
	special := map[string]any{}
	switch table {
	case "connectors":
		raw, hasSecrets := asString(row["secrets"])
		if row["secrets"] != nil {
			plain, err := secret.Decrypt(raw, e.key)
			testutil.Must(e.t, err)
			special["secrets"] = plain
		}
		config, _ := asString(row["config"])
		want := shaHex([]byte(config + "\x00" + map[bool]string{true: raw}[hasSecrets]))[:16]
		if h, _ := asString(row["config_hash"]); h == want {
			special["config_hash"] = "<config hash>"
		}
	case "hooks":
		enc, _ := asString(row["secret"])
		plain, err := secret.Decrypt(enc, e.key)
		testutil.Must(e.t, err)
		special["secret"] = plain
		id, _ := asString(row["id"])
		if slug, _ := asString(row["slug"]); len(id) >= 16 && slug == id[len(id)-16:] {
			special["slug"] = "<id tail>"
		}
	case "attachment_shares":
		special["token_hash"] = "<token hash>"
	case "saved_attachments":
		blob, _ := row["data"].([]byte)
		raw, err := secret.DecryptBytes(blob, e.key)
		testutil.Must(e.t, err)
		masked := fxRE.ReplaceAllFunc(bytes.Clone(raw), func(m []byte) []byte {
			return append([]byte("fx_"), bytes.Repeat([]byte("#"), len(m)-3)...)
		})
		special["data"] = map[string]any{"bytes": float64(len(masked)), "sha256": shaHex(masked)}
		if s, _ := asString(row["sha256"]); s == shaHex(raw) {
			special["sha256"] = "<sha256 of data>"
		}
	case "agents":
		if v, _ := row["is_default"].(int64); v == 1 {
			special["created_at"] = "<wall clock>"
			special["updated_at"] = "<wall clock>"
		}
	}
	out := make([]any, len(cols))
	for i, c := range cols {
		v, ok := special[c]
		if !ok {
			v = row[c]
		}
		switch x := v.(type) {
		case string:
			out[i] = e.n.str(x)
		case []byte:
			out[i] = map[string]any{"bytes": float64(len(x)), "sha256": shaHex(x)}
		case int64:
			out[i] = float64(x)
		case float64, bool, nil, map[string]any:
			out[i] = x
		default:
			e.t.Fatalf("%s.%s: unexpected %T", table, c, v)
		}
	}
	return out
}

func export(t testing.TB, d *db.DB, key string, sample bool) map[string]any {
	e := &exporter{t: t, d: d, key: key, n: &normalizer{seen: map[string]string{}, counts: map[string]int{}}}
	_, names := e.rows("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT IN ('_migrations', 'sqlite_sequence') ORDER BY name")
	tables := map[string]any{}
	for _, nr := range names {
		name, _ := asString(nr["name"])
		var columns []string
		_, info := e.rows(`SELECT name FROM pragma_table_info('` + name + `') ORDER BY cid`)
		for _, r := range info {
			c, _ := asString(r["name"])
			columns = append(columns, `"`+c+`"`)
		}
		plain := make([]any, len(columns))
		for i, c := range columns {
			plain[i] = strings.Trim(c, `"`)
		}
		names := make([]string, len(columns))
		for i := range columns {
			names[i] = plain[i].(string)
		}
		sel := "SELECT " + strings.Join(columns, ", ") + ` FROM "` + name + `"`
		if !sample {
			_, rows := e.rows(sel + " ORDER BY rowid")
			list := []any{}
			for _, r := range rows {
				list = append(list, e.rowValues(name, names, r))
			}
			tables[name] = map[string]any{"columns": plain, "rows": list}
			continue
		}
		var count int64
		testutil.Must(t, d.QueryRow(`SELECT count(*) FROM "`+name+`"`).Scan(&count))
		pick := func(order string) any {
			_, rows := e.rows(sel + " ORDER BY rowid " + order + " LIMIT 1")
			if len(rows) == 0 {
				return nil
			}
			return e.rowValues(name, names, rows[0])
		}
		first := pick("")
		last := pick("DESC")
		tables[name] = map[string]any{"columns": plain, "count": float64(count), "first": first, "last": last}
	}
	return tables
}

// diff reports the first difference between the golden's tables and Go's.
func diff(want, got map[string]any) string {
	var out []string
	for name, w := range want {
		g, ok := got[name]
		if !ok {
			out = append(out, "missing table "+name)
			continue
		}
		if reflect.DeepEqual(w, g) {
			continue
		}
		wt, gt := w.(map[string]any), g.(map[string]any)
		for key := range wt {
			if reflect.DeepEqual(wt[key], gt[key]) {
				continue
			}
			wr, wok := wt[key].([]any)
			gr, gok := gt[key].([]any)
			if key != "rows" || !wok || !gok {
				out = append(out, fmt.Sprintf("%s.%s:\n  want %s\n  got  %s", name, key, js(wt[key]), js(gt[key])))
				continue
			}
			if len(wr) != len(gr) {
				out = append(out, fmt.Sprintf("%s: %d rows, want %d", name, len(gr), len(wr)))
			}
			cols := wt["columns"].([]any)
			for i := 0; i < min(len(wr), len(gr)); i++ {
				if reflect.DeepEqual(wr[i], gr[i]) {
					continue
				}
				wc, gc := wr[i].([]any), gr[i].([]any)
				for j := range wc {
					if j < len(gc) && !reflect.DeepEqual(wc[j], gc[j]) {
						out = append(out, fmt.Sprintf("%s row %d %v:\n  want %s\n  got  %s", name, i, cols[j], js(wc[j]), js(gc[j])))
					}
				}
				break
			}
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			out = append(out, "extra table "+name)
		}
	}
	return strings.Join(out, "\n")
}

func js(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 400 {
		return string(b[:400]) + "..."
	}
	return string(b)
}

func applyGolden(t testing.TB, name string) *db.DB {
	t.Helper()
	d := testutil.DB(t)
	addOwner(t, d, goldenNow)
	testutil.Must(t, fixtures.Apply(d, name, fixtures.Options{Now: goldenNow, Force: true, MasterKey: testutil.TestKey}))
	return d
}

func TestFixturesMatchTheTSGoldens(t *testing.T) {
	for _, name := range fixtures.Names {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures-"+name+".json"))
			testutil.Must(t, err)
			var golden struct {
				Fixture string         `json:"fixture"`
				Now     int64          `json:"now"`
				Tables  map[string]any `json:"tables"`
			}
			testutil.Must(t, json.Unmarshal(b, &golden))
			if golden.Now != goldenNow || golden.Fixture != name {
				t.Fatalf("golden is for %s at %d", golden.Fixture, golden.Now)
			}
			d := applyGolden(t, name)
			got := export(t, d, testutil.TestKey, name == "huge")
			// Round-trip through JSON so both sides hold the same value types.
			gb, err := json.Marshal(got)
			testutil.Must(t, err)
			var norm map[string]any
			testutil.Must(t, json.Unmarshal(gb, &norm))
			if d := diff(golden.Tables, norm); d != "" {
				t.Fatalf("fixture %s differs from the TS golden:\n%s", name, d)
			}
		})
	}
}
