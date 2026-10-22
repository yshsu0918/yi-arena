// Colyseus does not persist anything for us: game records and chat go to Postgres via pg.
import pg from 'pg';

export const pool = new pg.Pool({
  host: process.env.DB_HOST || 'localhost',
  port: parseInt(process.env.DB_PORT || '5442', 10),
  user: 'postgres',
  password: 'localdb',
  database: 'colyseus',
});

export async function migrate() {
  await pool.query(`
    CREATE TABLE IF NOT EXISTS game_records (
      game_id    text PRIMARY KEY,
      game_type  text NOT NULL,
      players    jsonb NOT NULL,
      moves      jsonb NOT NULL,
      result     jsonb NOT NULL,
      started_at timestamptz NOT NULL,
      ended_at   timestamptz NOT NULL
    );
    CREATE TABLE IF NOT EXISTS chat_messages (
      id         bigserial PRIMARY KEY,
      room_id    text NOT NULL,
      sender     text NOT NULL,
      text       text NOT NULL,
      created_at timestamptz NOT NULL DEFAULT now()
    );
  `);
}

export interface GameRecord {
  game_id: string;
  game_type: string;
  players: { seat: number; order: 'first' | 'second'; username: string }[];
  moves: { n: number; player: string; x: number; y: number }[];
  result: { draw: boolean; winner: string | null };
  started_at: Date;
  ended_at: Date;
}

export async function saveGameRecord(r: GameRecord) {
  await pool.query(
    `INSERT INTO game_records (game_id, game_type, players, moves, result, started_at, ended_at)
     VALUES ($1, $2, $3, $4, $5, $6, $7)`,
    [r.game_id, r.game_type, JSON.stringify(r.players), JSON.stringify(r.moves), JSON.stringify(r.result), r.started_at, r.ended_at],
  );
}

export async function saveChat(roomId: string, sender: string, text: string) {
  await pool.query('INSERT INTO chat_messages (room_id, sender, text) VALUES ($1, $2, $3)', [roomId, sender, text]);
}
