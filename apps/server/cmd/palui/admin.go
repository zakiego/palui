package main

// The admin commands (apps/app/src/server/admin-cli.ts). Keep stdout clean:
// only the command's result goes there, so --json output can be piped.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/backup"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/manage"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/hooks"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/secret"
)

const usage = `usage: palui <command> [--json]
  serve                         run the server (HOST and PORT, default 0.0.0.0:3000;
                                --web-dir <dir> serves the dashboard from disk)
  version                       print the version
  owner invite                  print a single-use owner link (15 min). Also the
                                recovery path for a lost passkey
  connectors list               every connector with its state
  connectors on <id>            resume a connector
  connectors off <id>           pause a connector (keeps its credentials)
  connectors connect <type> [--id <id>] [--set key=value ...] [--secret-stdin <field>]
                                save and switch on a connector without the dashboard.
                                The secret is read from stdin so it stays out of shell history:
                                palui connectors connect telegram --secret-stdin botToken < token.txt
  backup <dest-path>            gzipped online snapshot of the database, safe while running
  secrets-check [--require-some]
                                decrypt every stored secret with MASTER_KEY, print counts only.
                                --require-some also fails when there was nothing to decrypt
  hook create <name> --chat <chatId> [--mode forward|summarize|flow]
                                create an inbound webhook and print its URL and secret once
  eval-reminders                the live 60-phrase reminder parse eval (makes model calls)`

// notReachable replaces "The runtime isn't reachable" now that there's one process.
const notReachable = "The server isn't running or didn't answer, so it picks this up when it starts. Check docker compose logs."

// cliIO is CliIo: everything a command touches, so tests can swap it.
type cliIO struct {
	db        func() (*db.DB, error)
	poke      func() bool
	appURL    func() string
	masterKey func() (string, error)
	dbFile    func() string
	out, err  io.Writer
	now       func() int64
	readStdin func() (string, error)
}

func (o *cliIO) println(s string)  { fmt.Fprintln(o.out, s) }
func (o *cliIO) eprintln(s string) { fmt.Fprintln(o.err, s) }

var (
	boolFlags  = map[string]bool{"--json": true, "--help": true, "-h": true, "--require-some": true}
	valueFlags = map[string]bool{"--chat": true, "--mode": true, "--id": true, "--secret-stdin": true}
	multiFlags = map[string]bool{"--set": true}
)

type parsed struct {
	positionals []string
	bools       map[string]bool
	values      map[string]string
	multi       map[string][]string
	unknown     []string
}

func parseArgs(argv []string) parsed {
	p := parsed{bools: map[string]bool{}, values: map[string]string{}, multi: map[string][]string{}}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		name, inline, hasInline := a, "", false
		if strings.HasPrefix(a, "--") && strings.Contains(a, "=") {
			j := strings.Index(a, "=")
			name, inline, hasInline = a[:j], a[j+1:], true
		}
		next := func() (string, bool) {
			if hasInline {
				return inline, true
			}
			if i+1 < len(argv) {
				i++
				return argv[i], true
			}
			return "", false
		}
		switch {
		case multiFlags[name]:
			if v, ok := next(); ok {
				p.multi[name] = append(p.multi[name], v)
			} else {
				p.unknown = append(p.unknown, name+" needs a value")
			}
		case valueFlags[name]:
			if v, ok := next(); ok {
				p.values[name] = v
			} else {
				p.unknown = append(p.unknown, name+" needs a value")
			}
		case boolFlags[a]:
			p.bools[a] = true
		case strings.HasPrefix(a, "-"):
			p.unknown = append(p.unknown, a)
		default:
			p.positionals = append(p.positionals, a)
		}
	}
	return p
}

func jsonLine(v any) string {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// ordered is a JSON object with keys in the order given (JS object literal order).
func ordered(kv ...any) *jsonx.Object {
	m := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i].(string), kv[i+1])
	}
	return m
}

func padEnd(s string, n int) string {
	for len([]rune(s)) < n {
		s += " "
	}
	return s
}

// runCLI is runCli.
func runCLI(argv []string, o *cliIO) int {
	p := parseArgs(argv)
	var cmd, sub string
	var rest []string
	if len(p.positionals) > 0 {
		cmd = p.positionals[0]
	}
	if len(p.positionals) > 1 {
		sub = p.positionals[1]
		rest = p.positionals[2:]
	}
	asJSON := p.bools["--json"]
	if o.now == nil {
		o.now = func() int64 { return time.Now().UnixMilli() }
	}
	if len(p.unknown) > 0 {
		o.eprintln("unknown flag: " + strings.Join(p.unknown, " ") + "\n" + usage)
		return 1
	}
	if cmd == "" || p.bools["--help"] || p.bools["-h"] {
		o.println(usage)
		return 0
	}
	code, err := runCommand(cmd, sub, rest, p, asJSON, o)
	if err != nil {
		if me, ok := manage.IsError(err); ok {
			o.eprintln(me.FieldsText())
			return 1
		}
		o.eprintln("error: " + err.Error())
		return 1
	}
	return code
}

func runCommand(cmd, sub string, rest []string, p parsed, asJSON bool, o *cliIO) (int, error) {
	switch {
	case cmd == "owner" && sub == "invite":
		d, err := o.db()
		if err != nil {
			return 1, err
		}
		recovery, err := auth.OwnerExists(d)
		if err != nil {
			return 1, err
		}
		inv, err := auth.CreateOwnerInvite(d, o.appURL(), o.now())
		if err != nil {
			return 1, err
		}
		if asJSON {
			o.println(jsonLine(ordered("url", inv.URL, "expiresAt", inv.ExpiresAt, "recovery", recovery)))
		} else {
			o.println(inv.URL)
			lead := "Owner link."
			if recovery {
				lead = "Recovery link: it removes the old passkeys and signs out other sessions."
			}
			o.eprintln(fmt.Sprintf("%s Works once, for %d minutes.", lead, auth.InviteTTLMs/60_000))
		}
		return 0, nil

	case cmd == "connectors":
		d, err := o.db()
		if err != nil {
			return 1, err
		}
		switch sub {
		case "list":
			rows, err := connectors.List(d)
			if err != nil {
				return 1, err
			}
			if asJSON {
				out := make([]*jsonx.Object, 0, len(rows))
				for _, r := range rows {
					m := jsonx.NewMap[any]()
					m.Set("id", r.ID)
					m.Set("type", r.Type)
					m.Set("kind", r.Kind)
					m.Set("enabled", r.Enabled)
					m.Set("status", r.Status)
					if r.StatusDetail != nil {
						m.Set("detail", *r.StatusDetail)
					} else {
						m.Set("detail", nil)
					}
					out = append(out, m)
				}
				o.println(jsonLine(out))
			} else if len(rows) == 0 {
				o.println("no connectors yet: add one in the dashboard (Connectors)")
			} else {
				for _, r := range rows {
					line := padEnd(r.ID, 20) + " " + padEnd(r.Kind, 8) + " " + r.Status
					if r.StatusDetail != nil && *r.StatusDetail != "" {
						line += " (" + *r.StatusDetail + ")"
					}
					o.println(line)
				}
			}
			return 0, nil
		case "on", "off":
			if len(rest) == 0 {
				o.eprintln("usage: palui connectors " + sub + " <id>")
				return 1, nil
			}
			id := rest[0]
			if sub == "on" {
				err = manage.Resume(d, id, "cli", o.now())
			} else {
				err = manage.Pause(d, id, "cli", o.now())
			}
			if err != nil {
				return 1, err
			}
			reachable := o.poke()
			state := "paused"
			if sub == "on" {
				state = "resumed"
			}
			if asJSON {
				o.println(jsonLine(ordered("id", id, "state", state, "runtimeReachable", reachable)))
			} else {
				o.println(id + " " + state)
			}
			if !reachable {
				o.eprintln(notReachable)
			}
			return 0, nil
		case "connect":
			if len(rest) == 0 {
				o.eprintln("usage: palui connectors connect <type> [--id <id>] [--set key=value ...] [--secret-stdin <field>]")
				return 1, nil
			}
			typ := rest[0]
			config := jsonx.NewMap[any]()
			for _, pair := range p.multi["--set"] {
				eq := strings.Index(pair, "=")
				if eq < 1 {
					o.eprintln(fmt.Sprintf("--set needs key=value, got %q", pair))
					return 1, nil
				}
				config.Set(pair[:eq], pair[eq+1:])
			}
			secrets := jsonx.NewMap[string]()
			if field := p.values["--secret-stdin"]; field != "" {
				if o.readStdin == nil {
					return 1, errors.New("This build can't read stdin.")
				}
				raw, err := o.readStdin()
				if err != nil {
					return 1, err
				}
				value := strings.TrimSpace(raw)
				if value == "" {
					o.eprintln("nothing on stdin for " + field)
					return 1, nil
				}
				secrets.Set(field, value)
			}
			id := p.values["--id"]
			if id == "" {
				id = typ
			}
			key, err := o.masterKey()
			if err != nil {
				return 1, err
			}
			if _, err := manage.SaveDraft(d, manage.DraftInput{ID: id, Type: typ, Config: config, Secrets: secrets}, key, o.now()); err != nil {
				return 1, err
			}
			if err := manage.Connect(d, id, key, o.now()); err != nil {
				return 1, err
			}
			reachable := o.poke()
			if asJSON {
				o.println(jsonLine(ordered("id", id, "type", typ, "state", "connecting", "runtimeReachable", reachable)))
			} else {
				o.println(id + " saved and switched on")
			}
			if !reachable {
				o.eprintln(notReachable)
			}
			return 0, nil
		}
		o.eprintln(usage)
		return 1, nil

	case cmd == "backup":
		dest := sub
		if dest == "" {
			o.eprintln("usage: palui backup <dest-path>   (e.g. /data/backups/palui.db.gz)")
			return 1, nil
		}
		res, err := backup.Backup(o.dbFile(), dest)
		if err != nil {
			return 1, err
		}
		if asJSON {
			o.println(jsonLine(res))
		} else {
			o.println(fmt.Sprintf("backup %s %d bytes (db %d bytes)", res.Dest, res.Bytes, res.DBBytes))
		}
		return 0, nil

	case cmd == "secrets-check":
		d, err := o.db()
		if err != nil {
			return 1, err
		}
		key, err := o.masterKey()
		if err != nil {
			return 1, err
		}
		rows, err := connectors.List(d)
		if err != nil {
			return 1, err
		}
		conns, secretsN, hookSecrets := 0, 0, 0
		failed := []string{}
		for _, row := range rows {
			if row.Secrets == nil || *row.Secrets == "" {
				continue
			}
			m, err := connectors.Secrets(row, key)
			if err != nil {
				failed = append(failed, "connector "+row.ID)
				continue
			}
			secretsN += m.Len()
			conns++
		}
		hrows, err := d.Query("SELECT slug, secret FROM hooks")
		if err != nil {
			return 1, err
		}
		type hookSecret struct{ slug, secret string }
		var hs []hookSecret
		for hrows.Next() {
			var h hookSecret
			if err := hrows.Scan(&h.slug, &h.secret); err != nil {
				hrows.Close()
				return 1, err
			}
			hs = append(hs, h)
		}
		hrows.Close()
		for _, h := range hs {
			if _, err := secret.Decrypt(h.secret, key); err != nil {
				failed = append(failed, "hook "+h.slug)
				continue
			}
			hookSecrets++
		}
		// An empty database decrypts nothing and fails nothing. The restore
		// drill passes --require-some so that can't count as proof the key fits.
		empty := p.bools["--require-some"] && secretsN+hookSecrets == 0
		ok := len(failed) == 0 && !empty
		if asJSON {
			m := jsonx.NewMap[any]()
			m.Set("ok", ok)
			m.Set("connectors", conns)
			m.Set("secrets", secretsN)
			m.Set("hooks", hookSecrets)
			m.Set("failed", failed)
			o.println(jsonLine(m))
		} else {
			o.println(fmt.Sprintf("decrypted %d secrets in %d connectors and %d hook secrets", secretsN, conns, hookSecrets))
			if len(failed) > 0 {
				o.eprintln("failed to decrypt: " + strings.Join(failed, ", ") + ". Is MASTER_KEY the one this database was written with?")
			}
			if empty {
				o.eprintln("nothing to decrypt: no connector or hook secrets in this database, so it proves nothing about MASTER_KEY")
			}
		}
		if ok {
			return 0, nil
		}
		return 1, nil

	case cmd == "hook" && sub == "create":
		name := strings.TrimSpace(strings.Join(rest, " "))
		chatID := p.values["--chat"]
		mode := p.values["--mode"]
		if mode == "" {
			mode = hooks.ModeForward
		}
		if name == "" || chatID == "" {
			o.eprintln("usage: palui hook create <name> --chat <chatId> [--mode forward|summarize|flow]")
			return 1, nil
		}
		if mode != hooks.ModeForward && mode != hooks.ModeSummarize && mode != hooks.ModeFlow {
			o.eprintln("--mode must be forward, summarize or flow")
			return 1, nil
		}
		d, err := o.db()
		if err != nil {
			return 1, err
		}
		if _, ok, err := chats.Get(d, chatID); err != nil {
			return 1, err
		} else if !ok {
			o.eprintln(fmt.Sprintf("no chat %q. Chat ids are on the Chats page.", chatID))
			return 1, nil
		}
		key, err := o.masterKey()
		if err != nil {
			return 1, err
		}
		created, err := hooks.Create(d, hooks.CreateInput{Name: name, ChatID: chatID, Mode: mode}, key, o.now())
		if err != nil {
			return 1, err
		}
		url := o.appURL() + "/api/hooks/" + created.Hook.Slug
		if asJSON {
			m := jsonx.NewMap[any]()
			m.Set("id", created.Hook.ID)
			m.Set("slug", created.Hook.Slug)
			m.Set("url", url)
			m.Set("secret", created.Secret)
			m.Set("mode", mode)
			o.println(jsonLine(m))
		} else {
			o.println(url)
			o.println(created.Secret)
			o.eprintln("That secret is shown once. Send x-palui-timestamp (unix seconds) and x-palui-signature: sha256=<hex HMAC-SHA256 of '<timestamp>.<raw body>'> with it.")
		}
		return 0, nil
	}
	sep := strings.TrimSpace(cmd + " " + sub)
	o.eprintln("unknown command: " + sep + "\n" + usage)
	return 1, nil
}

// pokeServer asks the running server to reconcile now (the old control API
// reconcile). It reaches the server on loopback at PORT with the admin token
// derived from MASTER_KEY; false when nothing answered.
func pokeServer(port, token string) bool {
	if token == "" {
		return false
	}
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+port+"/api/admin/reconcile", bytes.NewReader(nil))
	if err != nil {
		return false
	}
	req.Header.Set(httpapi.AdminHeader, token)
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == http.StatusOK
}
