// Section 6 DB verification: compare stored game records with what the E2E run played.
// Writes the query, raw output and check results to ../test-screenshot/db-check.txt.
import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const OUT = fileURLToPath(new URL('../test-screenshot/', import.meta.url));
const PROJECT = 'poc-nakama';

// Nakama keeps runtime storage objects in the `storage` table.
const QUERY = `SELECT key AS game_id, jsonb_pretty(value) AS record FROM storage WHERE collection = 'game_records' ORDER BY value->>'started_at';`;
// Optional (section 7): room chat persisted by Nakama's built-in channels.
const CHAT_QUERY = `SELECT stream_label AS room, username, content->>'text' AS text, create_time FROM message ORDER BY create_time;`;

function psql(sql, json = false) {
  const args = ['compose', '-p', PROJECT, 'exec', '-T', 'db', 'psql', '-U', 'postgres', '-d', 'nakama'];
  if (json) args.push('-At');
  return execFileSync('docker', [...args, '-c', sql], { encoding: 'utf8' });
}

// Normalise to the section 6 fields.
function loadRecords() {
  const rows = psql(`SELECT json_agg(value ORDER BY value->>'started_at') FROM storage WHERE collection = 'game_records';`, true).trim();
  return (JSON.parse(rows || 'null') || []).map((v) => ({
    game_id: v.game_id,
    game_type: v.game_type,
    players: v.players.map((p) => p.username),
    moves: v.moves.map((m) => [m.player, m.x, m.y]),
    winner: v.result.winner,
    started_at: v.started_at,
    ended_at: v.ended_at,
  }));
}

const expected = JSON.parse(readFileSync(OUT + 'e2e-result.json', 'utf8'));
if (!expected.games) throw new Error('e2e-result.json has no games; did the E2E run pass?');

const lines = [];
const log = (s = '') => { lines.push(s); console.log(s); };
let failed = 0;
const check = (desc, ok, detail = '') => {
  if (!ok) failed++;
  log(`${ok ? 'PASS' : 'FAIL'} ${desc}${detail ? ' — ' + detail : ''}`);
};

log(`# DB check (${PROJECT}) at ${new Date().toISOString()}`);
log();
log('## Query');
log(QUERY);
log();
log('## Result');
log(psql(QUERY).trimEnd());
log();
log('## Checks');

const records = loadRecords();
check('共有 2 筆對局紀錄（無缺漏、無重複）', records.length === 2, `${records.length} 筆`);
check('game_id 不重複', new Set(records.map((r) => r.game_id)).size === records.length);

for (const exp of expected.games) {
  const rec = records.find((r) => r.game_type === exp.game_type);
  const tag = `房間 ${exp.room}（${exp.game_type}）`;
  if (!rec) { check(`${tag} 有紀錄`, false); continue; }
  check(`${tag} 玩家與執子順序正確`, JSON.stringify(rec.players) === JSON.stringify(exp.players), rec.players.join(' → '));
  check(`${tag} 勝者與畫面一致`, rec.winner === exp.winner, `winner=${rec.winner}`);
  check(`${tag} 棋步數量一致`, rec.moves.length === exp.moves.length, `${rec.moves.length} 手`);
  check(`${tag} 棋步順序與內容一致`, JSON.stringify(rec.moves) === JSON.stringify(exp.moves),
    rec.moves.map(([p, x, y]) => `${p}(${x},${y})`).join(' '));
  check(`${tag} 有 started_at 與 ended_at 且順序正確`,
    !!rec.started_at && !!rec.ended_at && new Date(rec.started_at) <= new Date(rec.ended_at), `${rec.started_at} → ${rec.ended_at}`);
  const other = expected.games.find((g) => g !== exp);
  check(`${tag} 不含另一房間的玩家棋步`, !rec.moves.some(([p]) => other.players.includes(p)));
}

log();
log('## Chat persistence (optional, section 7)');
log(CHAT_QUERY);
log(psql(CHAT_QUERY).trimEnd());
log();
log(failed ? `${failed} CHECK(S) FAILED` : 'ALL DB CHECKS PASS');
writeFileSync(OUT + 'db-check.txt', lines.join('\n') + '\n');
process.exitCode = failed ? 1 : 0;
