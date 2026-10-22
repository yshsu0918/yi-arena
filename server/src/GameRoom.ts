// One game table. Game state is sent as plain JSON messages (not Schema state
// sync) so the client contract matches the other PoC branches.
import { Room, type Client } from '@colyseus/core';
import { applyMove, currentTurn, isGameType, newGame, validateMove, type GameState, type GameType, type Move, type Seat } from './rules.ts';
import { saveChat, saveGameRecord } from './db.ts';

const RECONNECT_SECONDS = 20;

type Status = 'waiting' | 'playing' | 'finished';
interface SeatInfo { name: string; sessionId: string; connected: boolean }

export interface RoomMetadata {
  gameType: GameType;
  host: string;
  status: Status;
  players: string[];
}

export class GameRoom extends Room {
  maxClients = 2;

  gameType!: GameType;
  host!: string;
  seats: (SeatInfo | null)[] = [null, null];
  status: Status = 'waiting';
  game!: GameState;
  startedAt: Date | null = null;
  endedAt: Date | null = null;

  onCreate(options: { gameType: GameType; name: string }) {
    if (!isGameType(options.gameType)) throw new Error('unknown gameType');
    this.gameType = options.gameType;
    this.host = options.name;
    this.game = newGame(this.gameType);
    this.updateMetadata();

    // The client asks for the snapshot once its handlers are registered.
    this.onMessage('ready', (client) => client.send('state', this.snapshot()));
    this.onMessage('move', (client, move: Move) => this.handleMove(client, move));
    this.onMessage('chat', (client, msg: { text: string }) => {
      const seat = this.seatOf(client);
      const text = String(msg?.text ?? '').slice(0, 200);
      if (seat < 0 || !text) return;
      const from = this.seats[seat]!.name;
      this.broadcast('chat', { from, text });
      saveChat(this.roomId, from, text).catch((e) => console.error('saveChat failed', e));
    });
  }

  onJoin(client: Client, options: { name: string }) {
    const free = this.seats[0] === null ? 0 : 1;
    this.seats[free] = { name: String(options.name), sessionId: client.sessionId, connected: true };
    if (this.status === 'waiting' && this.seats[0] && this.seats[1]) {
      this.status = 'playing';
      this.startedAt = new Date();
    }
    this.updateMetadata();
    this.broadcast('state', this.snapshot());
  }

  // Unexpected disconnect during a game: hold the seat for a while.
  async onDrop(client: Client) {
    const seat = this.seatOf(client);
    if (seat < 0 || this.status !== 'playing') return;
    this.seats[seat]!.connected = false;
    this.broadcast('state', this.snapshot());
    this.allowReconnection(client, RECONNECT_SECONDS);
  }

  onReconnect(client: Client) {
    const seat = this.seatOf(client);
    if (seat >= 0) this.seats[seat]!.connected = true;
    this.broadcast('state', this.snapshot());
  }

  onLeave(client: Client) {
    const seat = this.seatOf(client);
    if (seat < 0) return;
    if (this.status === 'waiting') this.seats[seat] = null;
    else this.seats[seat]!.connected = false;
    this.updateMetadata();
    this.broadcast('state', this.snapshot());
  }

  private handleMove(client: Client, move: Move) {
    const seat = this.seatOf(client) as Seat;
    let err: string | null;
    if (this.status !== 'playing') err = this.status === 'waiting' ? '等待對手加入' : '對局已結束';
    else if (seat < 0) err = '你不是這局的玩家';
    else err = validateMove(this.game, seat, move ?? { x: -1, y: -1 });
    if (err) {
      client.send('error', { message: err });
      return;
    }
    this.game = applyMove(this.game, seat, move);
    if (this.game.result.status !== 'ongoing') this.finish();
    this.broadcast('state', this.snapshot());
  }

  private finish() {
    this.status = 'finished';
    this.endedAt = new Date();
    this.lock();
    this.updateMetadata();
    const names = this.seats.map((s) => s!.name);
    const r = this.game.result;
    saveGameRecord({
      game_id: this.roomId,
      game_type: this.gameType,
      players: names.map((username, seat) => ({ seat, order: seat === 0 ? 'first' : 'second', username })),
      moves: this.game.moves.map((m, i) => ({ n: i + 1, player: names[i % 2], x: m.x, y: m.y })),
      result: r.status === 'draw' ? { draw: true, winner: null } : { draw: false, winner: names[r.winner!] },
      started_at: this.startedAt!,
      ended_at: this.endedAt,
    }).catch((e) => console.error('saveGameRecord failed', e));
  }

  private seatOf(client: Client): number {
    return this.seats.findIndex((s) => s?.sessionId === client.sessionId);
  }

  private updateMetadata() {
    const metadata: RoomMetadata = {
      gameType: this.gameType,
      host: this.host,
      status: this.status,
      players: this.seats.filter((s) => s).map((s) => s!.name),
    };
    this.setMetadata(metadata);
  }

  private snapshot() {
    const r = this.game.result;
    const seats = this.seats.map((s) => s?.name ?? null);
    return {
      gameType: this.gameType,
      size: this.game.size,
      board: this.game.board,
      seats,
      status: this.status,
      turn: currentTurn(this.game),
      moveCount: this.game.moves.length,
      result: r.status === 'ongoing' ? null : { draw: r.status === 'draw', winner: r.status === 'win' ? seats[r.winner!] : null, line: r.line ?? [] },
    };
  }
}
