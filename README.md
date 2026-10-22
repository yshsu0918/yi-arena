# PoC: Nakama

The Nakama branch of the PoC in `docs/poc-test-plan.md`.

| Path | Contents |
| --- | --- |
| `server/` | Nakama TypeScript runtime module (`src/rules.ts` game rules, `src/main.ts` match handler, RPC, chat hook) |
| `web/` | Vue frontend (`src/backend.ts` is the only Nakama-specific file) |
| `e2e/` | Playwright scenario (`run.mjs`) and DB verification (`db-check.mjs`) |
| `test-screenshot/` | Screenshots and results from the last run; see its README |

Run everything (needs Docker and Node 20+; first run also needs `npx playwright install chromium` in `e2e/`):

```sh
./scripts/test.sh                 # tick rate 1
TICK_RATE=10 ./scripts/test.sh    # compare latency
```

Ports: web + API on 8901, Postgres on 5441, Nakama console on 7351.
