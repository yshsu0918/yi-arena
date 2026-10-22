// boardgame.io game definitions wrapping the framework-free rules in rules.ts.
// Shared by the server and the Vue client (bgio runs moves on both sides).
import { ActivePlayers, INVALID_MOVE, TurnOrder } from 'boardgame.io/core';
import type { Game } from 'boardgame.io';
import { applyMove, newGame, validateMove, type GameState, type GameType, type Seat } from './rules.ts';

export interface G {
  game: GameState;
  seated: [boolean, boolean];
  startedAt: string | null;
  endedAt: string | null;
}

export interface SetupData { host: string }

// bgio starts a match as soon as it is created, so a "wait" phase holds the
// game until both players have connected and called `sit`.
function makeGame(gameType: GameType): Game<G, any, SetupData> {
  return {
    name: gameType,
    minPlayers: 2,
    maxPlayers: 2,
    setup: () => ({ game: newGame(gameType), seated: [false, false], startedAt: null, endedAt: null }),
    phases: {
      wait: {
        start: true,
        turn: { activePlayers: ActivePlayers.ALL },
        moves: {
          sit: ({ G, playerID }) => {
            G.seated[Number(playerID)] = true;
          },
        },
        endIf: ({ G }) => G.seated[0] && G.seated[1],
        next: 'play',
      },
      play: {
        turn: { order: TurnOrder.RESET, minMoves: 1, maxMoves: 1 },
        onBegin: ({ G }) => {
          G.startedAt = new Date().toISOString();
        },
        moves: {
          play: ({ G, playerID }, x: number, y: number) => {
            const seat = Number(playerID) as Seat;
            if (validateMove(G.game, seat, { x, y })) return INVALID_MOVE;
            const game = applyMove(G.game, seat, { x, y });
            return { ...G, game, endedAt: game.result.status === 'ongoing' ? null : new Date().toISOString() };
          },
        },
      },
    },
    endIf: ({ G }) => {
      const r = G.game.result;
      if (r.status === 'win') return { winner: String(r.winner) };
      if (r.status === 'draw') return { draw: true };
    },
  };
}

export const TicTacToe = makeGame('tictactoe');
export const Gomoku = makeGame('gomoku');
export const GAMES: Record<GameType, Game<G, any, SetupData>> = { tictactoe: TicTacToe, gomoku: Gomoku };
