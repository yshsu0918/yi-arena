# PoC: Colyseus

The Colyseus branch of the PoC in `docs/poc-test-plan.md`.

| Path | Contents |
| --- | --- |
| `server/` | Colyseus server (`src/rules.ts` game rules, `src/GameRoom.ts`, `src/Lobby.ts`, `src/db.ts`) |
| `web/` | Vue frontend (`src/backend.ts` is the only Colyseus-specific file) |
| `e2e/` | Playwright scenario (`run.mjs`) and DB verification (`db-check.mjs`) |
| `test-screenshot/` | Screenshots and results from the last run; see its README |

Run everything (needs Docker and Node 20+; first run also needs `npx playwright install chromium` in `e2e/`):

```sh
./scripts/test.sh
```

Ports: server + web on 8902, Postgres on 5442.
