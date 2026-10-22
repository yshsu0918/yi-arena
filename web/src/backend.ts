// boardgame.io adapter for the shared UI.
// - Guest login: the name is the Lobby API playerName (no auth).
// - Room list: Lobby API listMatches, polled every second (no push).
// - Player list: our own /presence heartbeat routes (bgio has none).
// - Room chat: bgio's built-in per-match chat.
// - Rejected moves: the server drops the reason, so the message comes from the
//   client's own run of the same reducer (state.transients.error).
import { Client, LobbyClient } from 'boardgame.io/client';
import { SocketIO } from 'boardgame.io/multiplayer';
import { GAMES, type G } from '../../server/src/games.ts';
import { validateMove, type Seat } from '../../server/src/rules.ts';
import type { Backend, ChatMessage, GameType, LobbySnapshot, RoomInfo, RoomView } from './types';

const POLL_MS = 1000;
const HEARTBEAT_MS = 2000;

const lobbyClient = new LobbyClient({ server: location.origin });

let me = '';
let rooms: RoomInfo[] = [];
let players: string[] = [];
let match: {
  gameName: GameType;
  matchID: string;
  playerID: string;
  credentials: string;
  client: ReturnType<typeof Client<G>>;
  lastMove: { x: number; y: number } | null;
  sitSent: boolean;
  seenChat: Set<string>;
  lastViewKey: string;
} | null = null;

let lobbyCb: (s: LobbySnapshot) => void = () => {};
let roomCb: (v: RoomView) => void = () => {};
let roomErrorCb: (m: string) => void = () => {};
let chatCb: (m: ChatMessage) => void = () => {};

async function poll() {
  try {
    const [lists, presence] = await Promise.all([
      Promise.all((['tictactoe', 'gomoku'] as GameType[]).map((g) => lobbyClient.listMatches(g))),
      fetch('/presence').then((r) => r.json()),
    ]);
    rooms = lists
      .flatMap((l) => l.matches)
      .map((m): RoomInfo => {
        const named = m.players.filter((p) => p.name);
        const finished = !!m.gameover;
        return {
          id: m.matchID,
          gameType: m.gameName as GameType,
          host: (m.setupData as any)?.host ?? '?',
          // A finished match is never left via the Lobby API (that would delete
          // it), and bgio's isConnected flag is not reliable when players
          // disconnect at the same moment, so nobody counts as in a finished room.
          players: finished ? [] : named.map((p) => p.name!),
          maxPlayers: m.players.length,
          status: finished ? 'finished' : named.length === m.players.length ? 'playing' : 'waiting',
        };
      })
      .sort((a, b) => a.host.localeCompare(b.host));
    players = presence.players;
    lobbyCb({ rooms, players: players.map((name) => ({ name })) });
  } catch (e) {
    console.warn('poll failed', e);
  }
}

function errorMessage(type: string, state: any): string {
  switch (type) {
    case 'action/inactive_player': return '還沒輪到你';
    case 'action/gameover': return '對局已結束';
    case 'action/unavailable_move': return '等待對手加入';
    case 'action/invalid_move': {
      const mv = match?.lastMove;
      return (mv && validateMove(state.G.game, Number(match!.playerID) as Seat, mv)) || '不合法的落子';
    }
    default: return type;
  }
}

function onState(state: any) {
  if (!match || !state) return;
  const c = match.client;
  const { G, ctx } = state;
  if (state.transients?.error) roomErrorCb(errorMessage(state.transients.error.type, state));

  // Take the seat once connected; the game starts when both players have sat.
  if (ctx.phase === 'wait' && !G.seated[Number(match.playerID)] && !match.sitSent) {
    match.sitSent = true;
    // This re-enters onState with the newer state; `state` here is now stale.
    c.moves.sit();
    return;
  }

  const seats = (c.matchData ?? []).map((p) => p.name ?? null);
  for (const m of c.chatMessages) {
    if (match.seenChat.has(m.id)) continue;
    match.seenChat.add(m.id);
    chatCb({ from: seats[Number(m.sender)] ?? `玩家 ${m.sender}`, text: m.payload.text });
  }

  // bgio notifies again right after a rejected move (to strip the error);
  // only emit when something visible changed so the error stays on screen.
  const viewKey = [state._stateID, ctx.phase, ctx.currentPlayer, !!ctx.gameover, seats.join(',')].join('|');
  if (state.transients?.error || viewKey === match.lastViewKey) return;
  match.lastViewKey = viewKey;
  const r = G.game.result;
  roomCb({
    gameType: match.gameName,
    size: G.game.size,
    board: G.game.board,
    seats,
    status: ctx.gameover ? 'finished' : ctx.phase === 'play' ? 'playing' : 'waiting',
    turn: Number(ctx.currentPlayer),
    moveCount: G.game.moves.length,
    result: r.status === 'ongoing' ? null : { draw: r.status === 'draw', winner: r.status === 'win' ? seats[r.winner] : null, line: r.line ?? [] },
  });
}

async function enter(gameName: GameType, matchID: string) {
  let joined;
  try {
    joined = await lobbyClient.joinMatch(gameName, matchID, { playerName: me });
  } catch (e: any) {
    const details = String(e?.details ?? '');
    if (/maximum number of players|not available/.test(details)) throw new Error(`房間已滿（${details}）`);
    throw new Error(details || e?.message);
  }
  const client = Client<G>({
    game: GAMES[gameName],
    multiplayer: SocketIO({ server: location.origin }),
    matchID,
    playerID: joined.playerID,
    credentials: joined.playerCredentials,
    debug: false,
  });
  match = { gameName, matchID, playerID: joined.playerID, credentials: joined.playerCredentials, client, lastMove: null, sitSent: false, seenChat: new Set(), lastViewKey: '' };
  client.subscribe(onState);
  client.start();
}

export const backend: Backend = {
  name: 'boardgame.io',

  async login(name) {
    me = name;
    const beat = () => fetch(`/presence?name=${encodeURIComponent(me)}`, { method: 'POST' });
    await beat();
    setInterval(beat, HEARTBEAT_MS);
    await poll();
    setInterval(poll, POLL_MS);
  },

  onLobby(cb) { lobbyCb = cb; },
  onRoom(cb) { roomCb = cb; },
  onRoomError(cb) { roomErrorCb = cb; },
  onChat(cb) { chatCb = cb; },

  async createRoom(gameType) {
    const { matchID } = await lobbyClient.createMatch(gameType, { numPlayers: 2, setupData: { host: me } });
    await enter(gameType, matchID);
  },

  async joinRoom(id) {
    const room = rooms.find((r) => r.id === id);
    if (!room) throw new Error('房間不存在');
    await enter(room.gameType, id);
  },

  async leaveRoom() {
    if (!match) return;
    const { client, gameName, matchID, playerID, credentials } = match;
    const finished = !!client.getState()?.ctx.gameover;
    client.stop();
    match = null;
    // Leaving a finished match would delete it (and its record) once both players have left.
    if (!finished) await lobbyClient.leaveMatch(gameName, matchID, { playerID, credentials });
    await poll();
  },

  move(x, y) {
    if (!match) return;
    match.lastMove = { x, y };
    match.client.moves.play(x, y);
  },

  sendChat(text) {
    match?.client.sendChatMessage({ text });
  },
};
