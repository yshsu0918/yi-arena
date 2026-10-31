// Command bots runs the PoC test scenarios A-E against a running stack and
// writes one JSON report per scenario. Each scenario expects an empty
// database (scripts/test.sh resets before each one).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yshsu0918/yi-arena/nakama-lobby-go-room/bots/nk"
)

func main() {
	scenarios := flag.String("scenario", "A", "scenarios to run, e.g. A or A,B,C")
	nakama := flag.String("nakama", "http://localhost:8920", "Nakama client API")
	roomStats := flag.String("room-stats", "http://localhost:8923/stats", "room service stats URL")
	compose := flag.String("compose", "docker compose -p poc-nakama-lobby-go-room -f ../docker-compose.yml", "docker compose command (scenario D)")
	outDir := flag.String("out", "../results", "directory for JSON reports")
	users := flag.Int("n", 100, "scenario E: users")
	pairs := flag.Int("pairs", 25, "scenario E: games")
	duration := flag.Duration("duration", 30*time.Second, "scenario E: measured window")
	dmEvery := flag.Duration("dm-every", 2*time.Second, "scenario E: average gap between one user's DMs")
	seed := flag.Uint64("seed", 1, "scenario E: random seed")
	flag.Parse()

	env := &Env{
		NK:        nk.NewClient(*nakama, getenv("NAKAMA_SERVER_KEY", "local-only-server-key")),
		HTTPKey:   getenv("NAKAMA_HTTP_KEY", "local-only-http-key"),
		RoomStats: *roomStats,
		Compose:   strings.Fields(*compose),
	}
	run := map[string]func(context.Context, *Env, *Report) error{
		"A": scenarioA, "B": scenarioB, "C": scenarioC, "D": scenarioD,
		"E": func(ctx context.Context, e *Env, r *Report) error {
			return scenarioE(ctx, e, r, loadConfig{Users: *users, Pairs: *pairs, Duration: *duration, DMEvery: *dmEvery, Seed: *seed})
		},
	}

	failed := false
	for _, name := range strings.Split(strings.ToUpper(*scenarios), ",") {
		fn, ok := run[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown scenario %q\n", name)
			os.Exit(2)
		}
		r := &Report{Scenario: name, StartedAt: time.Now()}
		fmt.Printf("== scenario %s\n", name)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		if err := fn(ctx, env, r); err != nil {
			var abort errAbort
			if !errors.As(err, &abort) {
				r.Check("-", "scenario error", false, "%v", err)
			} else {
				r.Check(abort.step, "中止：後續步驟依賴前一步", false, "")
			}
		}
		cancel()
		r.finish()
		path := filepath.Join(*outDir, "scenario-"+strings.ToLower(name)+".json")
		if *users != 100 && name == "E" {
			path = filepath.Join(*outDir, fmt.Sprintf("scenario-e-%d.json", *users))
		}
		b, _ := json.MarshalIndent(r, "", "  ")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		pass := 0
		for _, c := range r.Checks {
			if c.OK {
				pass++
			}
		}
		fmt.Printf("== scenario %s: %d/%d checks passed in %.1fs -> %s\n", name, pass, len(r.Checks), r.DurationSec, path)
		failed = failed || !r.Passed
	}
	if failed {
		os.Exit(1)
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
