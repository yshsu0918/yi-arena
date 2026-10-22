// Game rules module: pure logic, no Colyseus APIs.

export type GameType = 'tictactoe' | 'gomoku';
export type Seat = 0 | 1;

export interface Move { x: number; y: number }

export interface GameResult {
  status: 'ongoing' | 'win' | 'draw';
  winner?: Seat;
  line?: Move[];
}

export interface GameState {
  gameType: GameType;
  size: number;
  winLength: number;
  board: number[]; // -1 empty, 0 first player, 1 second player
  moves: Move[];
  result: GameResult;
}

export interface GameConfig { size: number; winLength: number }

const GAME_CONFIGS: { [type: string]: GameConfig } = {
  tictactoe: { size: 3, winLength: 3 },
  gomoku: { size: 15, winLength: 5 }, // freestyle: five or more wins
};

export function isGameType(t: any): t is GameType {
  return t === 'tictactoe' || t === 'gomoku';
}

export function newGame(gameType: GameType): GameState {
  var cfg = GAME_CONFIGS[gameType];
  var board: number[] = [];
  for (var i = 0; i < cfg.size * cfg.size; i++) board.push(-1);
  return { gameType: gameType, size: cfg.size, winLength: cfg.winLength, board: board, moves: [], result: { status: 'ongoing' } };
}

export function currentTurn(state: GameState): Seat {
  return (state.moves.length % 2) as Seat;
}

// Returns null when the move is legal, otherwise an error message.
export function validateMove(state: GameState, seat: Seat, move: Move): string | null {
  if (state.result.status !== 'ongoing') return '對局已結束';
  if (seat !== currentTurn(state)) return '還沒輪到你';
  if (typeof move.x !== 'number' || typeof move.y !== 'number' || move.x % 1 !== 0 || move.y % 1 !== 0) return '座標格式錯誤';
  if (move.x < 0 || move.y < 0 || move.x >= state.size || move.y >= state.size) return '超出棋盤';
  if (state.board[move.y * state.size + move.x] !== -1) return '這格已經有棋子';
  return null;
}

// Assumes validateMove passed. Returns a new state.
export function applyMove(state: GameState, seat: Seat, move: Move): GameState {
  var board = state.board.slice();
  board[move.y * state.size + move.x] = seat;
  var moves = state.moves.concat([{ x: move.x, y: move.y }]);
  var next: GameState = { gameType: state.gameType, size: state.size, winLength: state.winLength, board: board, moves: moves, result: { status: 'ongoing' } };
  next.result = computeResult(next, seat, move);
  return next;
}

export function computeResult(state: GameState, seat: Seat, last: Move): GameResult {
  var dirs = [[1, 0], [0, 1], [1, 1], [1, -1]];
  for (var d = 0; d < dirs.length; d++) {
    var line: Move[] = [{ x: last.x, y: last.y }];
    for (var sign = -1; sign <= 1; sign += 2) {
      var x = last.x + dirs[d][0] * sign, y = last.y + dirs[d][1] * sign;
      while (x >= 0 && y >= 0 && x < state.size && y < state.size && state.board[y * state.size + x] === seat) {
        line.push({ x: x, y: y });
        x += dirs[d][0] * sign;
        y += dirs[d][1] * sign;
      }
    }
    if (line.length >= state.winLength) return { status: 'win', winner: seat, line: line };
  }
  if (state.moves.length === state.size * state.size) return { status: 'draw' };
  return { status: 'ongoing' };
}

// Rebuild a state from a move list (reconnect / restore / record export).
export function replay(gameType: GameType, moves: Move[]): GameState {
  var s = newGame(gameType);
  for (var i = 0; i < moves.length; i++) {
    var seat = currentTurn(s);
    var err = validateMove(s, seat, moves[i]);
    if (err) throw new Error('move ' + i + ': ' + err);
    s = applyMove(s, seat, moves[i]);
  }
  return s;
}
