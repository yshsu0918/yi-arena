// Colyseus adapter for the shared UI.
// - Guest login: the name is passed as join options (no auth).
// - Room list: built-in LobbyRoom pushes "rooms" / "+" / "-" in real time.
// - Player list: our Lobby subclass pushes "players".
// - Room chat: plain room messages, broadcast to that room only.
import { Client, type Room } from '@colyseus/sdk';
import type { Backend, ChatMessage, GameType, LobbySnapshot, RoomInfo, RoomView } from './types';

const client = new Client(location.origin);

let me = '';
let lobby: Room;
let room: Room | null = null;
const rooms = new Map<string, RoomInfo>();
let players: string[] = [];

let lobbyCb: (s: LobbySnapshot) => void = () => {};
let roomCb: (v: RoomView) => void = () => {};
let roomErrorCb: (m: string) => void = () => {};
let chatCb: (m: ChatMessage) => void = () => {};

function emitLobby() {
  lobbyCb({
    rooms: [...rooms.values()].sort((a, b) => a.host.localeCompare(b.host)),
    players: players.map((name) => ({ name })),
  });
}

function toRoomInfo(r: any): RoomInfo {
  const m = r.metadata ?? {};
  return { id: r.roomId, gameType: m.gameType, host: m.host, players: m.players ?? [], maxPlayers: r.maxClients, status: m.status };
}

function attach(r: Room) {
  room = r;
  r.onMessage('state', (v: RoomView) => roomCb(v));
  r.onMessage('error', (e: { message: string }) => roomErrorCb(e.message));
  r.onMessage('chat', (m: ChatMessage) => chatCb(m));
  r.send('ready');
}

function friendlyError(e: any): Error {
  const msg = String(e?.message ?? e);
  if (/is locked|already full/.test(msg)) return new Error(`房間已滿（${msg}）`);
  return new Error(msg);
}

export const backend: Backend = {
  name: 'Colyseus',

  async login(name) {
    me = name;
    lobby = await client.joinOrCreate('lobby', { name });
    lobby.onMessage('rooms', (list: any[]) => {
      rooms.clear();
      for (const r of list) if (r.name === 'game') rooms.set(r.roomId, toRoomInfo(r));
      emitLobby();
    });
    lobby.onMessage('+', ([roomId, r]: [string, any]) => {
      if (r.name === 'game') rooms.set(roomId, toRoomInfo(r));
      emitLobby();
    });
    lobby.onMessage('-', (roomId: string) => {
      rooms.delete(roomId);
      emitLobby();
    });
    lobby.onMessage('players', (names: string[]) => {
      players = names;
      emitLobby();
    });
    lobby.send('ready');
  },

  onLobby(cb) { lobbyCb = cb; },
  onRoom(cb) { roomCb = cb; },
  onRoomError(cb) { roomErrorCb = cb; },
  onChat(cb) { chatCb = cb; },

  async createRoom(gameType: GameType) {
    attach(await client.create('game', { gameType, name: me }));
  },

  async joinRoom(id) {
    try {
      attach(await client.joinById(id, { name: me }));
    } catch (e) {
      throw friendlyError(e);
    }
  },

  async leaveRoom() {
    if (room) await room.leave();
    room = null;
  },

  move(x, y) {
    room?.send('move', { x, y });
  },

  sendChat(text) {
    room?.send('chat', { text });
  },
};
