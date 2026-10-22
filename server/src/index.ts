import { Origins, Server } from 'boardgame.io/server';
import { PostgresStore } from 'bgio-postgres';
import serve from 'koa-static';
import { fileURLToPath } from 'node:url';
import { Gomoku, TicTacToe } from './games.ts';

const PORT = parseInt(process.env.PORT || '8903', 10);
const DB_PORT = process.env.DB_PORT || '5443';
const WEB_DIST = fileURLToPath(new URL('../../web/dist/', import.meta.url));
const PRESENCE_TTL_MS = 5000;

const db = new PostgresStore(`postgresql://postgres:localdb@localhost:${DB_PORT}/bgio`, { logging: false });

const server = Server({
  games: [TicTacToe, Gomoku],
  db,
  origins: [Origins.LOCALHOST],
});

// boardgame.io has no notion of who is online. Clients POST a heartbeat
// every couple of seconds and the lobby lists names seen recently.
const lastSeen = new Map<string, number>();
server.router.post('/presence', (ctx) => {
  const name = String(ctx.query.name ?? '').slice(0, 20);
  if (name) lastSeen.set(name, Date.now());
  ctx.body = {};
});
server.router.get('/presence', (ctx) => {
  const now = Date.now();
  for (const [name, t] of lastSeen) if (now - t > PRESENCE_TTL_MS) lastSeen.delete(name);
  ctx.body = { players: [...lastSeen.keys()].sort() };
});

server.app.use(serve(WEB_DIST));

server.run(PORT, () => console.log(`boardgame.io PoC listening on http://localhost:${PORT}`));
