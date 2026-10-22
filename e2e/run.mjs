// End-to-end scenario from docs/poc-test-plan.md (S0–S8), five players.
// Usage: BASE_URL=http://localhost:8903 node run.mjs
// Writes screenshots and e2e-result.json into ../test-screenshot/.
import { chromium } from 'playwright';
import { mkdirSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8903';
const OUT = fileURLToPath(new URL('../test-screenshot/', import.meta.url));
const VIEWPORT = { width: 1280, height: 800 };
const TIMEOUT = 15000;

mkdirSync(OUT, { recursive: true });

const checks = [];
function check(step, desc, ok, detail = '') {
  checks.push({ step, desc, ok, detail });
  console.log(`${ok ? 'PASS' : 'FAIL'} [${step}] ${desc}${detail ? ' — ' + detail : ''}`);
  if (!ok) throw new Error(`[${step}] ${desc} ${detail}`);
}

async function waitFor(desc, fn, timeout = TIMEOUT) {
  const start = Date.now();
  let last;
  while (Date.now() - start < timeout) {
    try {
      last = await fn();
      if (last) return last;
    } catch (e) {
      last = e;
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`timeout waiting for: ${desc} (last: ${last})`);
}

const browser = await chromium.launch();
const players = {};
for (let i = 1; i <= 5; i++) {
  const context = await browser.newContext({ viewport: VIEWPORT, locale: 'zh-TW' });
  const page = await context.newPage();
  page.on('pageerror', (e) => console.log(`[P${i} pageerror]`, e.message));
  if (process.env.DEBUG) page.on('console', (m) => console.log(`[P${i} console]`, m.text()));
  players[`P${i}`] = { name: `Player${i}`, page };
}
const { P1, P2, P3, P4, P5 } = players;

async function shot(file, p) {
  await p.page.screenshot({ path: OUT + file });
  console.log('  screenshot', file);
}

// Side-by-side image of two players' views, rendered by the browser itself.
async function shotPair(file, a, b) {
  const [ia, ib] = await Promise.all([a.page.screenshot(), b.page.screenshot()]);
  const page = await browser.newPage({ viewport: { width: 2 * VIEWPORT.width + 30, height: VIEWPORT.height + 40 } });
  const img = (buf, label) =>
    `<figure><figcaption>${label}</figcaption><img src="data:image/png;base64,${buf.toString('base64')}"></figure>`;
  await page.setContent(`<style>body{margin:0;display:flex;gap:10px;padding:5px;background:#333;font:16px sans-serif;color:#fff}
    figure{margin:0}figcaption{height:24px}img{display:block;width:${VIEWPORT.width}px}</style>
    ${img(ia, `${a.name} 視角`)}${img(ib, `${b.name} 視角`)}`);
  await page.screenshot({ path: OUT + file });
  await page.close();
  console.log('  screenshot', file);
}

const rows = (p) => p.page.getByTestId('room-row');
const rowOf = (p, host) => p.page.locator(`[data-testid="room-row"][data-host="${host}"]`);
const playerNames = (p) => p.page.getByTestId('player-row').locator('td:first-child').allInnerTexts();
const cell = (p, x, y) => p.page.getByTestId(`cell-${x}-${y}`);
const statusText = (p) => p.page.getByTestId('status').innerText();

async function hasPlayerList(p) {
  return (await p.page.getByTestId('players-na').count()) === 0;
}

async function login(p) {
  await p.page.goto(BASE_URL);
  await p.page.getByTestId('name').fill(p.name);
  await p.page.getByTestId('enter').click();
  await p.page.getByTestId('lobby').waitFor({ timeout: TIMEOUT });
}

// Times include Playwright's click and frame-polling overhead (a few ms).
const latencies = [];    // click -> stone on the mover's screen
const oppLatencies = []; // click -> stone on the opponent's screen (always a server round trip)
// Click a cell and wait until both players see the stone.
async function move(me, opp, x, y) {
  const t0 = Date.now();
  await cell(me, x, y).click();
  await Promise.all([
    stoneVisible(me, x, y).then(() => latencies.push(Date.now() - t0)),
    stoneVisible(opp, x, y).then(() => oppLatencies.push(Date.now() - t0)),
  ]);
}

// Checked inside the page on every animation frame (~16 ms resolution).
function stoneVisible(p, x, y) {
  return p.page.waitForFunction(
    (id) => document.querySelector(`[data-testid="${id}"]`)?.textContent.trim() !== '',
    `cell-${x}-${y}`,
    { polling: 'raf', timeout: TIMEOUT },
  );
}

async function expectRejectedMove(me, x, y, step, desc) {
  const before = await cell(me, x, y).innerText();
  await cell(me, x, y).click();
  const err = await waitFor(`room error for ${me.name}`, async () => {
    const loc = me.page.getByTestId('room-error');
    return (await loc.count()) ? (await loc.innerText()) : null;
  });
  const after = await cell(me, x, y).innerText();
  check(step, desc, after === before, `錯誤訊息「${err}」`);
}

async function chat(p, text) {
  await p.page.getByTestId('chat-input').fill(text);
  await p.page.getByTestId('chat-send').click();
}
const chatLog = (p) => p.page.getByTestId('chat-msg').allInnerTexts();

try {
  // S0: everyone enters the lobby.
  for (const p of [P1, P2, P3, P4, P5]) await login(p);
  const playerList = await hasPlayerList(P5);
  if (playerList) {
    await waitFor('5 players in P1/P5 player list', async () =>
      (await playerNames(P1)).length === 5 && (await playerNames(P5)).length === 5);
  }
  check('S0', '五位玩家進入大廳、房間列表為空', (await rows(P5).count()) === 0);
  check('S0', '玩家列表顯示 5 人', !playerList || (await playerNames(P5)).length === 5, playerList ? '' : 'N/A');
  await shot('01-lobby-initial-p1.png', P1);
  await shot('01-lobby-initial-p5.png', P5);

  // S1: P1 creates tic-tac-toe room A, P3 creates gomoku room B.
  await P1.page.getByTestId('create-tictactoe').click();
  await P3.page.getByTestId('create-gomoku').click();
  await P1.page.getByTestId('room').waitFor();
  await P3.page.getByTestId('room').waitFor();
  await waitFor('P5 sees 2 waiting rooms', async () =>
    (await rowOf(P5, P1.name).getAttribute('data-status')) === 'waiting' &&
    (await rowOf(P5, P3.name).getAttribute('data-status')) === 'waiting');
  const rowA = await rowOf(P5, P1.name).innerText();
  const rowB = await rowOf(P5, P3.name).innerText();
  check('S1', '房間列表出現 2 個房間並標示遊戲與等待中',
    (await rows(P5).count()) === 2 && rowA.includes('井字棋') && rowB.includes('五子棋') && rowA.includes('等待中'), `${rowA} | ${rowB}`);
  await shot('02-roomlist-waiting-p5.png', P5);

  // S2: P2 joins A, P4 joins B from the room list.
  await rowOf(P2, P1.name).getByTestId('join').click();
  await rowOf(P4, P3.name).getByTestId('join').click();
  await P2.page.getByTestId('room').waitFor();
  await P4.page.getByTestId('room').waitFor();
  await waitFor('P5 sees both rooms 2/2', async () =>
    (await rowOf(P5, P1.name).getByTestId('room-size').innerText()) === '2/2' &&
    (await rowOf(P5, P3.name).getByTestId('room-size').innerText()) === '2/2');
  check('S2', '兩個房間人數變為 2/2', true);
  await shot('03-roomlist-full-p5.png', P5);
  await waitFor('room A playing', async () => (await P2.page.getByTestId('status').getAttribute('data-status')) === 'playing');
  await waitFor('room B playing', async () => (await P4.page.getByTestId('status').getAttribute('data-status')) === 'playing');
  await shotPair('03-room-joined-a.png', P1, P2);
  await shotPair('03-room-joined-b.png', P3, P4);

  // S3: P5 tries to join the full room A.
  await rowOf(P5, P1.name).getByTestId('join').click();
  const err = await waitFor('P5 join error', async () => {
    const loc = P5.page.getByTestId('lobby-error');
    return (await loc.count()) ? loc.innerText() : null;
  });
  check('S3', 'P5 加入已滿房間被拒絕', (await P5.page.getByTestId('lobby').count()) === 1, `錯誤訊息「${err}」`);
  await shot('04-join-full-rejected-p5.png', P5);
  check('S3', '房間 A 對局不受影響', (await P1.page.getByTestId('status').getAttribute('data-status')) === 'playing');

  // S4: both games started; lobby shows them as playing.
  await waitFor('P5 sees both rooms playing', async () =>
    (await rowOf(P5, P1.name).getAttribute('data-status')) === 'playing' &&
    (await rowOf(P5, P3.name).getAttribute('data-status')) === 'playing');
  check('S4', '兩房間進入對局、大廳顯示進行中', true, `A: ${await statusText(P1)} / B: ${await statusText(P3)}`);
  await shot('05-game-start-a-p1.png', P1);
  await shot('05-game-start-b-p3.png', P3);
  await shot('06-lobby-ingame-p5.png', P5);

  // S5: both rooms play at the same time, including illegal moves.
  const movesA = [];
  const movesB = [];
  const playA = async () => {
    await expectRejectedMove(P2, 1, 1, 'S5', 'A 房：還沒輪到 P2 時落子被拒絕');
    await move(P1, P2, 0, 0); movesA.push([P1.name, 0, 0]);
    await expectRejectedMove(P2, 0, 0, 'S5', 'A 房：落在已有棋子的格子被拒絕');
    await move(P2, P1, 0, 1); movesA.push([P2.name, 0, 1]);
    await move(P1, P2, 1, 0); movesA.push([P1.name, 1, 0]);
    await move(P2, P1, 1, 1); movesA.push([P2.name, 1, 1]);
  };
  const playB = async () => {
    await expectRejectedMove(P4, 7, 8, 'S5', 'B 房：還沒輪到 P4 時落子被拒絕');
    const seq = [[P3, P4, 7, 7], [P4, P3, 7, 8], [P3, P4, 8, 7], [P4, P3, 8, 8], [P3, P4, 9, 7], [P4, P3, 9, 8], [P3, P4, 10, 7], [P4, P3, 10, 8]];
    for (const [me, opp, x, y] of seq) {
      await move(me, opp, x, y);
      movesB.push([me.name, x, y]);
    }
  };
  await Promise.all([playA(), playB()]);
  const stonesA = await P1.page.getByTestId('board').locator('.cell').allInnerTexts();
  const stonesB = await P3.page.getByTestId('board').locator('.cell').allInnerTexts();
  check('S5', '兩房間同時落子、棋盤互不干擾',
    stonesA.filter((s) => s.trim()).length === 4 && stonesB.filter((s) => s.trim()).length === 8,
    `A 有 ${stonesA.filter((s) => s.trim()).length} 子，B 有 ${stonesB.filter((s) => s.trim()).length} 子`);
  await shotPair('07-game-playing-a.png', P1, P2);
  await shotPair('07-game-playing-b.png', P3, P4);

  // S6: room chat and isolation.
  await chat(P1, 'A房 P1：你好！');
  await chat(P3, 'B房 P3：大家好');
  await waitFor('P2 gets P1 msg', async () => (await chatLog(P2)).some((m) => m.includes('A房 P1')));
  await chat(P2, 'A房 P2：收到，請多指教');
  await waitFor('P4 gets P3 msg', async () => (await chatLog(P4)).some((m) => m.includes('B房 P3')));
  await chat(P4, 'B房 P4：你好');
  await waitFor('A both msgs', async () => (await chatLog(P1)).length === 2 && (await chatLog(P2)).length === 2);
  await waitFor('B both msgs', async () => (await chatLog(P3)).length === 2 && (await chatLog(P4)).length === 2);
  await new Promise((r) => setTimeout(r, 1000)); // give any leaked message time to arrive
  const logA = [...(await chatLog(P1)), ...(await chatLog(P2))];
  const logB = [...(await chatLog(P3)), ...(await chatLog(P4))];
  check('S6', 'A 房雙方互相收到訊息', (await chatLog(P1)).length === 2 && (await chatLog(P2)).length === 2);
  check('S6', 'B 房雙方互相收到訊息', (await chatLog(P3)).length === 2 && (await chatLog(P4)).length === 2);
  check('S6', 'A 房訊息不出現在 B 房、B 房訊息不出現在 A 房',
    !logA.some((m) => m.includes('B房')) && !logB.some((m) => m.includes('A房')));
  const p5Text = await P5.page.locator('body').innerText();
  check('S6', 'P5（大廳）看不到任何房內訊息', !p5Text.includes('A房') && !p5Text.includes('B房'));
  await shotPair('08-chat-a.png', P1, P2);
  await shotPair('08-chat-b.png', P3, P4);

  // S7: P1 and P3 win.
  await Promise.all([
    move(P1, P2, 2, 0).then(() => movesA.push([P1.name, 2, 0])),
    move(P3, P4, 11, 7).then(() => movesB.push([P3.name, 11, 7])),
  ]);
  for (const [p, room] of [[P1, 'A'], [P2, 'A'], [P3, 'B'], [P4, 'B']]) {
    await waitFor(`${p.name} sees finished`, async () => (await p.page.getByTestId('status').getAttribute('data-status')) === 'finished');
  }
  const [sA1, sA2, sB3, sB4] = await Promise.all([statusText(P1), statusText(P2), statusText(P3), statusText(P4)]);
  check('S7', 'A 房雙方顯示 Player1 獲勝', sA1.includes('Player1 獲勝') && sA1.includes('你贏了') && sA2.includes('Player1 獲勝') && sA2.includes('你輸了'), `${sA1} / ${sA2}`);
  check('S7', 'B 房雙方顯示 Player3 獲勝', sB3.includes('Player3 獲勝') && sB3.includes('你贏了') && sB4.includes('Player3 獲勝') && sB4.includes('你輸了'), `${sB3} / ${sB4}`);
  await shot('09-game-over-a-winner.png', P1);
  await shot('09-game-over-a-loser.png', P2);
  await shot('09-game-over-b-winner.png', P3);
  await shot('09-game-over-b-loser.png', P4);

  // S8: everyone returns to the lobby.
  for (const p of [P1, P2, P3, P4]) {
    await p.page.getByTestId('back').click();
    await p.page.getByTestId('lobby').waitFor();
  }
  await waitFor('finished rooms removed or marked finished', async () => {
    for (const p of [P1, P5]) {
      const st = await rows(p).evaluateAll((els) => els.map((e) => e.getAttribute('data-status')));
      if (st.some((s) => s !== 'finished')) return false;
    }
    return true;
  });
  const roomsLeft = await rows(P5).count();
  check('S8', '已結束的房間被移除或標示已結束', true, roomsLeft === 0 ? '房間已移除' : `剩 ${roomsLeft} 間，皆標示已結束`);
  if (playerList) {
    await waitFor('all 5 players in lobby', async () => {
      const st = await P5.page.getByTestId('player-row').locator('td:nth-child(2)').allInnerTexts();
      return st.length === 5 && st.every((s) => s === '大廳');
    });
  }
  check('S8', '玩家列表顯示 5 人皆在大廳', true, playerList ? '' : 'N/A');
  await shot('10-lobby-after-p1.png', P1);
  await shot('10-lobby-after-p5.png', P5);

  const stats = (xs) => ({ avg: Math.round(xs.reduce((a, b) => a + b, 0) / xs.length), min: Math.min(...xs), max: Math.max(...xs), samples: xs.length });
  const avg = stats(latencies).avg;
  const result = {
    baseUrl: BASE_URL,
    finishedAt: new Date().toISOString(),
    playerList,
    moveLatencyMs: stats(latencies),
    opponentLatencyMs: stats(oppLatencies),
    games: [
      { room: 'A', game_type: 'tictactoe', players: [P1.name, P2.name], winner: P1.name, moves: movesA },
      { room: 'B', game_type: 'gomoku', players: [P3.name, P4.name], winner: P3.name, moves: movesB },
    ],
    checks,
  };
  writeFileSync(OUT + 'e2e-result.json', JSON.stringify(result, null, 2));
  console.log(`\nALL PASS (${checks.length} checks). move latency avg ${avg} ms, opponent sees it after avg ${stats(oppLatencies).avg} ms`);
} catch (e) {
  console.error('\nE2E FAILED:', e.message);
  for (const [k, p] of Object.entries(players)) await p.page.screenshot({ path: OUT + `failure-${k}.png` }).catch(() => {});
  writeFileSync(OUT + 'e2e-result.json', JSON.stringify({ failed: e.message, checks }, null, 2));
  process.exitCode = 1;
} finally {
  await browser.close();
}
