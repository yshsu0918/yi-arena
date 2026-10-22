import { Server } from '@colyseus/core';
import { WebSocketTransport } from '@colyseus/ws-transport';
import express from 'express';
import { fileURLToPath } from 'node:url';
import { GameRoom } from './GameRoom.ts';
import { Lobby } from './Lobby.ts';
import { migrate } from './db.ts';

const PORT = parseInt(process.env.PORT || '8902', 10);
const WEB_DIST = fileURLToPath(new URL('../../web/dist/', import.meta.url));

const server = new Server({
  transport: new WebSocketTransport(),
  greet: false,
  express: (app) => {
    app.use(express.static(WEB_DIST));
  },
});

server.define('lobby', Lobby);
server.define('game', GameRoom).enableRealtimeListing();

await migrate();
await server.listen(PORT);
console.log(`colyseus PoC listening on http://localhost:${PORT}`);
