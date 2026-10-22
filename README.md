# PoC: boardgame.io

The boardgame.io branch of the PoC in `docs/poc-test-plan.md`.

| Path | Contents |
| --- | --- |
| `server/` | bgio server (`src/rules.ts` game rules, `src/games.ts` bgio game definitions shared with the client, `src/index.ts` server + presence routes) |
| `web/` | Vue frontend (`src/backend.ts` is the only bgio-specific file) |
| `e2e/` | Playwright scenario (`run.mjs`) and DB verification (`db-check.mjs`) |
| `test-screenshot/` | Screenshots and results from the last run; see its README |

Run everything (needs Docker and Node 20+; first run also needs `npx playwright install chromium` in `e2e/`):

```sh
./scripts/test.sh
```

Ports: server + web on 8903, Postgres on 5443.
