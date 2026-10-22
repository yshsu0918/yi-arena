// Runs the bgio game definitions through bgio's local (single-process) client.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Client } from 'boardgame.io/client';
import { Local } from 'boardgame.io/multiplayer';
import { TicTacToe } from '../src/games.ts';

// The local master processes actions asynchronously.
const flush = () => new Promise((r) => setTimeout(r, 10));

async function players() {
  const multiplayer = Local();
  const p = ['0', '1'].map((playerID) => Client({ game: TicTacToe, multiplayer, playerID, debug: false } as any));
  p.forEach((c) => c.start());
  await flush();
  return p;
}

test('moves are blocked until both players sit, then player 0 starts', async () => {
  const [a, b] = await players();
  a.moves.play(0, 0);
  await flush();
  assert.equal(a.getState()!.G.game.moves.length, 0);
  a.moves.sit();
  await flush();
  b.moves.sit();
  await flush();
  assert.equal(a.getState()!.ctx.phase, 'play');
  assert.equal(a.getState()!.ctx.currentPlayer, '0');
  assert.ok(a.getState()!.G.startedAt);
});

test('out-of-turn and occupied-cell moves are rejected; a line ends the game', async () => {
  const [a, b] = await players();
  a.moves.sit();
  await flush();
  b.moves.sit();
  await flush();
  b.moves.play(1, 1); // not b's turn
  await flush();
  assert.equal(a.getState()!.G.game.moves.length, 0);
  a.moves.play(0, 0);
  await flush();
  b.moves.play(0, 0); // occupied
  await flush();
  assert.equal(a.getState()!.G.game.moves.length, 1);
  b.moves.play(0, 1);
  await flush();
  a.moves.play(1, 0);
  await flush();
  b.moves.play(1, 1);
  await flush();
  a.moves.play(2, 0);
  await flush();
  const s = a.getState()!;
  assert.deepEqual(s.ctx.gameover, { winner: '0' });
  assert.ok(s.G.endedAt);
});
