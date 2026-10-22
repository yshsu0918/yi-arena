// Nakama adapter for the shared UI.
// - Guest login: authenticateCustom with a name-derived id.
// - Player list: presences of the built-in "lobby" chat channel.
// - Room list: listMatches (authoritative matches + JSON label), polled every second.
// - Room chat: built-in room chat channel named after the match id.
import { Client, type Session, type Socket } from '@heroiclabs/nakama-js';
import type { Backend, ChatMessage, GameType, LobbySnapshot, RoomInfo, RoomView } from './types';

const OP_MOVE = 1;
const OP_STATE = 2;
const OP_ERROR = 3;
const POLL_MS = 1000;

const useSSL = location.protocol === 'https:';
const client = new Client('defaultkey', location.hostname, location.port || (useSSL ? '443' : '80'), useSSL);

let session: Session;
let socket: Socket;
let lobbyChannelId = '';
const lobbyPresences = new Map<string, string>(); // userId -> username
let rooms: RoomInfo[] = [];
let matchId: string | null = null;
let roomChannelId: string | null = null;

let lobbyCb: (s: LobbySnapshot) => void = () => {};
let roomCb: (v: RoomView) => void = () => {};
let roomErrorCb: (m: string) => void = () => {};
let chatCb: (m: ChatMessage) => void = () => {};

function emitLobby() {
  const players = [...lobbyPresences.values()].sort().map((name) => ({ name }));
  lobbyCb({ rooms, players });
}

async function pollRooms() {
  try {
    const list = await client.listMatches(session, 100, true, undefined, 0, 2);
    rooms = (list.matches ?? [])
      .map((m) => {
        const label = JSON.parse(m.label || '{}');
        return { id: m.match_id!, gameType: label.gameType, host: label.host, players: label.players, maxPlayers: 2, status: label.status };
      })
      .sort((a, b) => a.host.localeCompare(b.host));
    emitLobby();
  } catch (e) {
    console.warn('listMatches failed', e);
  }
}

async function enterMatch(id: string) {
  // Set before joining: the server's first state broadcast (from matchJoin)
  // can arrive before the joinMatch promise resolves.
  matchId = id;
  try {
    await socket.joinMatch(id);
  } catch (e) {
    matchId = null;
    throw e;
  }
  const ch = await socket.joinChat(id, 1, true, false);
  roomChannelId = ch.id;
}

export const backend: Backend = {
  name: 'Nakama',

  async login(name) {
    session = await client.authenticateCustom('guest-' + name, true, name);
    socket = client.createSocket(useSSL, false);
    socket.onmatchdata = (md) => {
      if (md.match_id !== matchId) return;
      const payload = JSON.parse(new TextDecoder().decode(md.data));
      if (md.op_code === OP_STATE) roomCb(payload);
      else if (md.op_code === OP_ERROR) roomErrorCb(payload.message);
    };
    socket.onchannelpresence = (e) => {
      if (e.channel_id !== lobbyChannelId) return;
      for (const p of e.leaves ?? []) lobbyPresences.delete(p.user_id);
      for (const p of e.joins ?? []) lobbyPresences.set(p.user_id, p.username);
      emitLobby();
    };
    socket.onchannelmessage = (m) => {
      if (m.channel_id !== roomChannelId) return;
      chatCb({ from: m.username!, text: (m.content as any).text });
    };
    await socket.connect(session, true);
    const lobby = await socket.joinChat('lobby', 1, false, false);
    lobbyChannelId = lobby.id;
    for (const p of lobby.presences ?? []) lobbyPresences.set(p.user_id, p.username);
    lobbyPresences.set(lobby.self.user_id, lobby.self.username);
    await pollRooms();
    setInterval(pollRooms, POLL_MS);
  },

  onLobby(cb) { lobbyCb = cb; },
  onRoom(cb) { roomCb = cb; },
  onRoomError(cb) { roomErrorCb = cb; },
  onChat(cb) { chatCb = cb; },

  async createRoom(gameType: GameType) {
    const res = await client.rpc(session, 'create_room', { gameType });
    await enterMatch((res.payload as any).matchId);
  },

  async joinRoom(id) {
    await enterMatch(id);
  },

  async leaveRoom() {
    if (roomChannelId) await socket.leaveChat(roomChannelId);
    if (matchId) await socket.leaveMatch(matchId);
    roomChannelId = null;
    matchId = null;
    await pollRooms();
  },

  move(x, y) {
    if (matchId) socket.sendMatchState(matchId, OP_MOVE, JSON.stringify({ x, y }));
  },

  sendChat(text) {
    if (roomChannelId) socket.writeChatMessage(roomChannelId, { text });
  },
};
