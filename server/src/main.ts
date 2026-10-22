// Nakama runtime module: lobby/room glue around the rules in rules.ts.
// Nakama's JS runtime requires handlers to be top-level named functions.

var OP_MOVE = 1;   // client -> server: {x, y}
var OP_STATE = 2;  // server -> clients: full room snapshot
var OP_ERROR = 3;  // server -> one client: {message}

var SYSTEM_USER = '00000000-0000-0000-0000-000000000000';
var RECORD_COLLECTION = 'game_records';
var EMPTY_ROOM_SECONDS = 30;

interface SeatInfo { userId: string; username: string; presence: nkruntime.Presence | null }

interface RoomState {
  gameType: GameType;
  host: string;
  seats: (SeatInfo | null)[];
  status: 'waiting' | 'playing' | 'finished';
  game: GameState;
  startedAt: string | null;
  endedAt: string | null;
  tickRate: number;
  emptyTicks: number;
}

function InitModule(ctx: nkruntime.Context, logger: nkruntime.Logger, nk: nkruntime.Nakama, initializer: nkruntime.Initializer) {
  initializer.registerMatch<RoomState>('room', {
    matchInit: matchInit,
    matchJoinAttempt: matchJoinAttempt,
    matchJoin: matchJoin,
    matchLeave: matchLeave,
    matchLoop: matchLoop,
    matchTerminate: matchTerminate,
    matchSignal: matchSignal,
  });
  initializer.registerRpc('create_room', rpcCreateRoom);
  initializer.registerRtBefore('ChannelJoin', beforeChannelJoin);
  logger.info('yi-arena module loaded');
}

function rpcCreateRoom(ctx: nkruntime.Context, logger: nkruntime.Logger, nk: nkruntime.Nakama, payload: string): string {
  var req = JSON.parse(payload || '{}');
  if (!isGameType(req.gameType)) throw new Error('unknown gameType');
  var matchId = nk.matchCreate('room', { gameType: req.gameType, host: ctx.username });
  return JSON.stringify({ matchId: matchId });
}

// Room chat uses Nakama's built-in room channels named after the match id.
// Anyone could join a room channel by name, so only seated players may join.
function beforeChannelJoin(ctx: nkruntime.Context, logger: nkruntime.Logger, nk: nkruntime.Nakama, envelope: nkruntime.EnvelopeChannelJoin): nkruntime.EnvelopeChannelJoin {
  var target = envelope.channelJoin.target || '';
  if (target === 'lobby') return envelope;
  var match = nk.matchGet(target);
  if (!match) throw new Error('room not found');
  var label = JSON.parse(match.label);
  if (label.players.indexOf(ctx.username) < 0) throw new Error('not a member of this room');
  return envelope;
}

function matchInit(ctx: nkruntime.Context, logger: nkruntime.Logger, nk: nkruntime.Nakama, params: { [key: string]: any }) {
  var gameType: GameType = params.gameType;
  var tickRate = parseInt(ctx.env['TICK_RATE'] || '1', 10);
  var state: RoomState = {
    gameType: gameType,
    host: params.host,
    seats: [null, null],
    status: 'waiting',
    game: newGame(gameType),
    startedAt: null,
    endedAt: null,
    tickRate: tickRate,
    emptyTicks: 0,
  };
  return { state: state, tickRate: tickRate, label: makeLabel(state) };
}

function makeLabel(state: RoomState): string {
  var players: string[] = [];
  for (var i = 0; i < state.seats.length; i++) {
    var s = state.seats[i];
    if (s) players.push(s.username);
  }
  return JSON.stringify({ gameType: state.gameType, host: state.host, status: state.status, players: players });
}

function seatOf(state: RoomState, userId: string): number {
  for (var i = 0; i < state.seats.length; i++) {
    var s = state.seats[i];
    if (s && s.userId === userId) return i;
  }
  return -1;
}

function matchJoinAttempt(ctx: nkruntime.Context, logger: nkruntime.Logger, nk: nkruntime.Nakama, dispatcher: nkruntime.MatchDispatcher, tick: number, state: RoomState, presence: nkruntime.Presence, metadata: { [key: string]: any }) {
  // A seated player coming back (reconnect) keeps their seat.
  if (seatOf(state, presence.userId) >= 0) return { state: state, accept: true };
  if (state.status === 'finished') return { state: state, accept: false, rejectMessage: '對局已結束' };
  var free = state.seats[0] === null ? 0 : state.seats[1] === null ? 1 : -1;
  if (free < 0) return { state: state, accept: false, rejectMessage: '房間已滿' };
  // Reserve the seat now so two attempts in the same tick cannot both get it.
  state.seats[free] = { userId: presence.userId, username: presence.username, presence: null };
  dispatcher.matchLabelUpdate(makeLabel(state));
  return { state: state, accept: true };
}

function matchJoin(ctx: nkruntime.Context, logger: nkruntime.Logger, nk: nkruntime.Nakama, dispatcher: nkruntime.MatchDispatcher, tick: number, state: RoomState, presences: nkruntime.Presence[]) {
  for (var i = 0; i < presences.length; i++) {
    var seat = seatOf(state, presences[i].userId);
    if (seat >= 0) state.seats[seat]!.presence = presences[i];
  }
  if (state.status === 'waiting' && state.seats[0] && state.seats[1]) {
    state.status = 'playing';
    state.startedAt = new Date().toISOString();
  }
  dispatcher.matchLabelUpdate(makeLabel(state));
  broadcastState(dispatcher, state);
  return { state: state };
}

function matchLeave(ctx: nkruntime.Context, logger: nkruntime.Logger, nk: nkruntime.Nakama, dispatcher: nkruntime.MatchDispatcher, tick: number, state: RoomState, presences: nkruntime.Presence[]) {
  for (var i = 0; i < presences.length; i++) {
    var seat = seatOf(state, presences[i].userId);
    if (seat < 0) continue;
    if (state.status === 'waiting') state.seats[seat] = null; // free the seat before the game starts
    else state.seats[seat]!.presence = null;                    // keep it for reconnect
  }
  dispatcher.matchLabelUpdate(makeLabel(state));
  broadcastState(dispatcher, state);
  return { state: state };
}

function matchLoop(ctx: nkruntime.Context, logger: nkruntime.Logger, nk: nkruntime.Nakama, dispatcher: nkruntime.MatchDispatcher, tick: number, state: RoomState, messages: nkruntime.MatchMessage[]) {
  for (var i = 0; i < messages.length; i++) {
    var m = messages[i];
    if (m.opCode !== OP_MOVE) continue;
    var seat = seatOf(state, m.sender.userId) as Seat;
    var err: string | null;
    var move: Move = { x: -1, y: -1 };
    if (state.status !== 'playing') err = state.status === 'waiting' ? '等待對手加入' : '對局已結束';
    else if (seat < 0) err = '你不是這局的玩家';
    else {
      try { move = JSON.parse(nk.binaryToString(m.data)); } catch (e) { move = { x: -1, y: -1 }; }
      err = validateMove(state.game, seat, move);
    }
    if (err) {
      dispatcher.broadcastMessage(OP_ERROR, JSON.stringify({ message: err }), [m.sender]);
      continue;
    }
    state.game = applyMove(state.game, seat, move);
    if (state.game.result.status !== 'ongoing') finishGame(ctx, nk, logger, dispatcher, state);
    broadcastState(dispatcher, state);
  }

  var connected = 0;
  for (var j = 0; j < state.seats.length; j++) if (state.seats[j] && state.seats[j]!.presence) connected++;
  if (connected > 0) state.emptyTicks = 0;
  else state.emptyTicks++;
  if (state.status === 'finished' && connected === 0) return null;
  if (state.emptyTicks > EMPTY_ROOM_SECONDS * state.tickRate) return null;
  return { state: state };
}

function finishGame(ctx: nkruntime.Context, nk: nkruntime.Nakama, logger: nkruntime.Logger, dispatcher: nkruntime.MatchDispatcher, state: RoomState) {
  state.status = 'finished';
  state.endedAt = new Date().toISOString();
  dispatcher.matchLabelUpdate(makeLabel(state));
  var r = state.game.result;
  var players: any[] = [];
  for (var i = 0; i < state.seats.length; i++) {
    players.push({ seat: i, order: i === 0 ? 'first' : 'second', userId: state.seats[i]!.userId, username: state.seats[i]!.username });
  }
  var moves: any[] = [];
  for (var k = 0; k < state.game.moves.length; k++) {
    moves.push({ n: k + 1, player: state.seats[k % 2]!.username, x: state.game.moves[k].x, y: state.game.moves[k].y });
  }
  var record = {
    game_id: ctx.matchId,
    game_type: state.gameType,
    players: players,
    moves: moves,
    result: r.status === 'draw' ? { draw: true, winner: null } : { draw: false, winner: state.seats[r.winner!]!.username },
    started_at: state.startedAt,
    ended_at: state.endedAt,
  };
  nk.storageWrite([{ collection: RECORD_COLLECTION, key: ctx.matchId!, userId: SYSTEM_USER, value: record, permissionRead: 2, permissionWrite: 0 }]);
  logger.info('game record written: %s', ctx.matchId);
}

function broadcastState(dispatcher: nkruntime.MatchDispatcher, state: RoomState) {
  var r = state.game.result;
  var seats: (string | null)[] = [];
  for (var i = 0; i < state.seats.length; i++) seats.push(state.seats[i] ? state.seats[i]!.username : null);
  dispatcher.broadcastMessage(OP_STATE, JSON.stringify({
    gameType: state.gameType,
    size: state.game.size,
    board: state.game.board,
    seats: seats,
    status: state.status,
    turn: currentTurn(state.game),
    moveCount: state.game.moves.length,
    result: r.status === 'ongoing' ? null : { draw: r.status === 'draw', winner: r.status === 'win' ? seats[r.winner!] : null, line: r.line || [] },
  }));
}

function matchTerminate(ctx: nkruntime.Context, logger: nkruntime.Logger, nk: nkruntime.Nakama, dispatcher: nkruntime.MatchDispatcher, tick: number, state: RoomState, graceSeconds: number) {
  return { state: state };
}

function matchSignal(ctx: nkruntime.Context, logger: nkruntime.Logger, nk: nkruntime.Nakama, dispatcher: nkruntime.MatchDispatcher, tick: number, state: RoomState, data: string) {
  return { state: state, data: data };
}
