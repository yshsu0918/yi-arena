// Built-in LobbyRoom (realtime room listing) plus a player list, which
// Colyseus does not provide: we track who is connected to the lobby ourselves.
import { LobbyRoom, type Client } from '@colyseus/core';

export class Lobby extends LobbyRoom {
  players = new Map<string, string>(); // sessionId -> name

  async onCreate(options: any) {
    await super.onCreate(options);
    this.onMessage('ready', (client) => {
      client.send('rooms', this.rooms);
      client.send('players', this.playerNames());
    });
  }

  onJoin(client: Client, options: any) {
    super.onJoin(client as any, options);
    this.players.set(client.sessionId, String(options?.name ?? '?'));
    this.broadcast('players', this.playerNames());
  }

  onLeave(client: Client) {
    super.onLeave(client as any);
    this.players.delete(client.sessionId);
    this.broadcast('players', this.playerNames());
  }

  private playerNames() {
    return [...this.players.values()].sort();
  }
}
