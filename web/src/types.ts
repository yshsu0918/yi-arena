// UI contract shared by all PoC branches. Only backend.ts differs per framework.

export type GameType = 'tictactoe' | 'gomoku';
export type RoomStatus = 'waiting' | 'playing' | 'finished';

export interface RoomInfo {
  id: string;
  gameType: GameType;
  host: string;
  players: string[];
  maxPlayers: number;
  status: RoomStatus;
}

export interface PlayerInfo {
  name: string;
}

export interface LobbySnapshot {
  rooms: RoomInfo[];
  players: PlayerInfo[] | null; // null = framework has no player list (N/A)
}

export interface RoomView {
  gameType: GameType;
  size: number;
  board: number[]; // -1 empty, 0 first player, 1 second player
  seats: (string | null)[];
  status: RoomStatus;
  turn: number;
  moveCount: number;
  result: { draw: boolean; winner: string | null; line: { x: number; y: number }[] } | null;
}

export interface ChatMessage {
  from: string;
  text: string;
}

export interface Backend {
  readonly name: string; // framework name shown in the header
  login(name: string): Promise<void>;
  onLobby(cb: (s: LobbySnapshot) => void): void;
  createRoom(gameType: GameType): Promise<void>;
  joinRoom(roomId: string): Promise<void>;
  leaveRoom(): Promise<void>;
  onRoom(cb: (v: RoomView) => void): void;
  onRoomError(cb: (message: string) => void): void;
  onChat(cb: (m: ChatMessage) => void): void;
  move(x: number, y: number): void;
  sendChat(text: string): void;
}

export const GAME_NAMES: Record<GameType, string> = { tictactoe: '井字棋', gomoku: '五子棋' };
export const STATUS_NAMES: Record<RoomStatus, string> = { waiting: '等待中', playing: '進行中', finished: '已結束' };
