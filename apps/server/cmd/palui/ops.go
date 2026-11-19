package main

// Commands the image and the ops scripts use, so neither needs Node, a shell
// tool or the sqlite3 CLI:
//
//   palui healthcheck [--port P] [--timeout D] [--print]
//     the compose healthcheck: GET http://127.0.0.1:$PORT/health, exit 0 only
//     on a 200 (any other status, a refused connection or a timeout is 1).
//     --print writes the body to stdout, whatever the status: the image has no
//     shell or wget, so this is how scripts/restore-drill.sh (and a person
//     debugging) read /health from inside the container
//   palui snapshot <sqlite-file> <dest.gz> [--json]
//     a consistent gzipped copy of any SQLite file while it is being written
//     (VACUUM INTO, like `palui backup`); scripts/backup.sh uses it for the
//     WhatsApp stores under $DATA_DIR/wa/

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/backup"
	"github.com/zakiego/palui/apps/server/internal/serve"
)

// healthcheckTimeout stays under compose's healthcheck timeout (5 s).
const healthcheckTimeout = 4 * time.Second

func runHealthcheck(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	_, port := serve.ListenAddr(getenv)
	p := fs.String("port", port, "port the server listens on (PORT)")
	timeout := fs.Duration("timeout", healthcheckTimeout, "give up after this long")
	printBody := fs.Bool("print", false, "write the /health body to stdout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	url := "http://" + net.JoinHostPort("127.0.0.1", *p) + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	// No proxy, ever: this is the loopback.
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	if *printBody {
		fmt.Fprintln(stdout, strings.TrimRight(string(body), "\n"))
	}
	if res.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "healthcheck: %s answered %d\n", url, res.StatusCode)
		return 1
	}
	return 0
}

func runSnapshot(args []string, stdout, stderr io.Writer) int {
	var pos []string
	asJSON := false
	for _, a := range args {
		switch {
		case a == "--json":
			asJSON = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintln(stderr, "unknown flag: "+a+"\nusage: palui snapshot <sqlite-file> <dest.gz> [--json]")
			return 2
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) != 2 {
		fmt.Fprintln(stderr, "usage: palui snapshot <sqlite-file> <dest.gz> [--json]")
		return 2
	}
	res, err := backup.Backup(pos[0], pos[1])
	if err != nil {
		fmt.Fprintln(stderr, "snapshot failed:", err)
		return 1
	}
	if asJSON {
		b, _ := json.Marshal(res)
		fmt.Fprintln(stdout, string(b))
	} else {
		fmt.Fprintf(stdout, "snapshot %s %d bytes (db %d bytes)\n", res.Dest, res.Bytes, res.DBBytes)
	}
	return 0
}

// runtimeOffEnv reads PALUI_NO_RUNTIME: "1", "true", "yes" or "on" turn the
// runtime off.
func runtimeOffEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
