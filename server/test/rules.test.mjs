// Runs the compiled ES5 bundle in a VM context and tests the rule functions.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const ctx = vm.createContext({});
vm.runInContext(readFileSync(new URL('../build/index.js', import.meta.url), 'utf8'), ctx);
const { newGame, validateMove, applyMove, currentTurn, replay } = ctx;

const play = (type, moves) => replay(type, moves.map(([x, y]) => ({ x, y })));

test('tictactoe: row of three wins for first player', () => {
  const s = play('tictactoe', [[0, 0], [0, 1], [1, 0], [1, 1], [2, 0]]);
  assert.equal(s.result.status, 'win');
  assert.equal(s.result.winner, 0);
  assert.equal(s.result.line.length, 3);
});

test('tictactoe: full board without line is a draw', () => {
  const s = play('tictactoe', [[0, 0], [1, 0], [2, 0], [1, 1], [0, 1], [0, 2], [1, 2], [2, 1], [2, 2]]);
  assert.equal(s.result.status, 'draw');
});

test('rejects wrong turn, occupied cell, out of board, and moves after game end', () => {
  let s = newGame('tictactoe');
  assert.equal(validateMove(s, 1, { x: 0, y: 0 }), '還沒輪到你');
  s = applyMove(s, 0, { x: 0, y: 0 });
  assert.equal(currentTurn(s), 1);
  assert.equal(validateMove(s, 1, { x: 0, y: 0 }), '這格已經有棋子');
  assert.equal(validateMove(s, 1, { x: 3, y: 0 }), '超出棋盤');
  assert.equal(validateMove(s, 1, { x: 0.5, y: 0 }), '座標格式錯誤');
  const done = play('tictactoe', [[0, 0], [0, 1], [1, 0], [1, 1], [2, 0]]);
  assert.equal(validateMove(done, 1, { x: 2, y: 2 }), '對局已結束');
});

test('gomoku: horizontal, vertical and both diagonals win with five', () => {
  for (const [dx, dy] of [[1, 0], [0, 1], [1, 1], [1, -1]]) {
    const moves = [];
    for (let i = 0; i < 5; i++) {
      moves.push([5 + dx * i, 7 + dy * i]);
      if (i < 4) moves.push([0 + i, 0]); // white plays on the top row, no five
    }
    const s = play('gomoku', moves);
    assert.equal(s.result.status, 'win', `dir ${dx},${dy}`);
    assert.equal(s.result.winner, 0);
  }
});

test('gomoku: four in a row is not a win; freestyle overline (six) wins', () => {
  const four = play('gomoku', [[0, 7], [0, 8], [1, 7], [1, 8], [2, 7], [2, 8], [3, 7]]);
  assert.equal(four.result.status, 'ongoing');
  // Black has 0-2 and 4-5 on row 7, then fills 3 to make six.
  const six = play('gomoku', [[0, 7], [0, 9], [1, 7], [1, 9], [2, 7], [2, 9], [4, 7], [9, 9], [5, 7], [10, 9], [3, 7]]);
  assert.equal(six.result.status, 'win');
  assert.equal(six.result.line.length, 6);
});

test('a line crossing the board edge does not wrap around', () => {
  // Black at x=13,14 on row 7 and x=0,1,2 on row 8 would be five if indices wrapped.
  const s = play('gomoku', [[13, 7], [5, 0], [14, 7], [6, 0], [0, 8], [7, 0], [1, 8], [8, 0], [2, 8]]);
  assert.equal(s.result.status, 'ongoing');
});
