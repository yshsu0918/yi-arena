<script setup lang="ts">
import { computed, ref } from 'vue';
import { backend } from './backend';
import { GAME_NAMES, STATUS_NAMES, type ChatMessage, type GameType, type LobbySnapshot, type RoomView } from './types';

const screen = ref<'login' | 'lobby' | 'room'>('login');
const me = ref('');
const nameInput = ref('');
const lobby = ref<LobbySnapshot>({ rooms: [], players: [] });
const room = ref<RoomView | null>(null);
const chat = ref<ChatMessage[]>([]);
const chatInput = ref('');
const lobbyError = ref('');
const roomError = ref('');
const busy = ref(false);

backend.onLobby((s) => (lobby.value = s));
backend.onRoom((v) => {
  room.value = v;
  roomError.value = '';
});
backend.onRoomError((m) => (roomError.value = m));
backend.onChat((m) => chat.value.push(m));

async function run(fn: () => Promise<void>, onError: (m: string) => void) {
  busy.value = true;
  try {
    await fn();
  } catch (e: any) {
    onError(e?.message || String(e));
  } finally {
    busy.value = false;
  }
}

function enter() {
  const name = nameInput.value.trim();
  if (!name) return;
  run(async () => {
    await backend.login(name);
    me.value = name;
    screen.value = 'lobby';
  }, (m) => (lobbyError.value = m));
}

function enterRoom(fn: () => Promise<void>) {
  lobbyError.value = '';
  run(async () => {
    room.value = null;
    chat.value = [];
    roomError.value = '';
    await fn();
    screen.value = 'room';
  }, (m) => (lobbyError.value = '無法加入房間：' + m));
}

const create = (t: GameType) => enterRoom(() => backend.createRoom(t));
const join = (id: string) => enterRoom(() => backend.joinRoom(id));

function back() {
  run(async () => {
    await backend.leaveRoom();
    room.value = null;
    screen.value = 'lobby';
  }, (m) => (roomError.value = m));
}

function send() {
  const text = chatInput.value.trim();
  if (!text) return;
  backend.sendChat(text);
  chatInput.value = '';
}

function playerStatus(name: string): string {
  const r = lobby.value.rooms.find((r) => r.players.includes(name));
  if (!r) return '大廳';
  return r.status === 'playing' ? `對局中（${GAME_NAMES[r.gameType]}）` : r.status === 'waiting' ? '等待對手' : '房內（已結束）';
}

const mySeat = computed(() => (room.value ? room.value.seats.indexOf(me.value) : -1));

function stone(v: number): string {
  if (v < 0 || !room.value) return '';
  if (room.value.gameType === 'tictactoe') return v === 0 ? '✕' : '○';
  return v === 0 ? '●' : '○';
}

function seatLabel(seat: number): string {
  if (!room.value) return '';
  return room.value.gameType === 'tictactoe' ? (seat === 0 ? '✕ 先手' : '○ 後手') : seat === 0 ? '● 黑（先手）' : '○ 白（後手）';
}

const statusText = computed(() => {
  const v = room.value;
  if (!v) return '連線中…';
  if (v.status === 'waiting') return '等待對手加入…';
  if (v.result) {
    if (v.result.draw) return '和局';
    const mine = v.result.winner === me.value ? '你贏了！' : mySeat.value >= 0 ? '你輸了' : '';
    return `${v.result.winner} 獲勝 ${mine}`;
  }
  const who = v.seats[v.turn];
  return who === me.value ? `輪到你（${seatLabel(v.turn)}）` : `輪到 ${who}（${seatLabel(v.turn)}）`;
});

function isWinCell(x: number, y: number): boolean {
  return !!room.value?.result?.line.some((p) => p.x === x && p.y === y);
}
</script>

<template>
  <header>
    <strong>Yi Arena PoC · {{ backend.name }}</strong>
    <span v-if="me" data-testid="me">玩家：{{ me }}</span>
  </header>

  <main v-if="screen === 'login'" class="login" data-testid="login">
    <h1>訪客登入</h1>
    <form @submit.prevent="enter">
      <input v-model="nameInput" data-testid="name" placeholder="訪客名稱" maxlength="20" />
      <button data-testid="enter" :disabled="busy">進入大廳</button>
    </form>
    <p v-if="lobbyError" class="error">{{ lobbyError }}</p>
  </main>

  <main v-else-if="screen === 'lobby'" class="lobby" data-testid="lobby">
    <section>
      <h2>大廳列表</h2>
      <table>
        <thead><tr><th>大廳</th><th>線上人數</th></tr></thead>
        <tbody>
          <tr data-testid="lobby-row"><td>主大廳</td><td>{{ lobby.players ? lobby.players.length : 'N/A' }}</td></tr>
        </tbody>
      </table>
    </section>

    <section>
      <h2>房間列表 <small data-testid="room-count">（{{ lobby.rooms.length }}）</small></h2>
      <div class="actions">
        <button data-testid="create-tictactoe" :disabled="busy" @click="create('tictactoe')">建立井字棋房間</button>
        <button data-testid="create-gomoku" :disabled="busy" @click="create('gomoku')">建立五子棋房間</button>
      </div>
      <p v-if="lobbyError" class="error" data-testid="lobby-error">{{ lobbyError }}</p>
      <table>
        <thead><tr><th>遊戲</th><th>房主</th><th>玩家</th><th>人數</th><th>狀態</th><th></th></tr></thead>
        <tbody>
          <tr v-if="!lobby.rooms.length"><td colspan="6" class="empty">目前沒有房間</td></tr>
          <tr v-for="r in lobby.rooms" :key="r.id" data-testid="room-row" :data-host="r.host" :data-status="r.status">
            <td>{{ GAME_NAMES[r.gameType] }}</td>
            <td>{{ r.host }}</td>
            <td>{{ r.players.join('、') }}</td>
            <td data-testid="room-size">{{ r.players.length }}/{{ r.maxPlayers }}</td>
            <td :class="'st-' + r.status">{{ STATUS_NAMES[r.status] }}</td>
            <td><button data-testid="join" :disabled="busy || r.status === 'finished'" @click="join(r.id)">加入</button></td>
          </tr>
        </tbody>
      </table>
    </section>

    <section>
      <h2>玩家列表</h2>
      <p v-if="!lobby.players" data-testid="players-na">此框架無玩家列表（N/A）</p>
      <table v-else>
        <thead><tr><th>玩家</th><th>狀態</th></tr></thead>
        <tbody>
          <tr v-for="p in lobby.players" :key="p.name" data-testid="player-row">
            <td>{{ p.name }}<span v-if="p.name === me">（你）</span></td>
            <td>{{ playerStatus(p.name) }}</td>
          </tr>
        </tbody>
      </table>
    </section>
  </main>

  <main v-else class="room" data-testid="room">
    <div class="board-col">
      <h2>{{ room ? GAME_NAMES[room.gameType] : '' }}房間</h2>
      <div class="seats" v-if="room">
        <span v-for="(s, i) in room.seats" :key="i" :class="{ turn: room.status === 'playing' && room.turn === i }">
          {{ seatLabel(i) }}：{{ s ?? '（空位）' }}
        </span>
      </div>
      <p class="status" data-testid="status" :data-status="room?.status">{{ statusText }}</p>
      <p v-if="roomError" class="error" data-testid="room-error">{{ roomError }}</p>
      <div
        v-if="room"
        class="board"
        :class="room.gameType"
        :style="{ gridTemplateColumns: `repeat(${room.size}, var(--cell))` }"
        data-testid="board"
      >
        <template v-for="y in room.size" :key="y">
          <button
            v-for="x in room.size"
            :key="x"
            class="cell"
            :class="{ win: isWinCell(x - 1, y - 1), black: room.board[(y - 1) * room.size + x - 1] === 0 }"
            :data-testid="`cell-${x - 1}-${y - 1}`"
            @click="backend.move(x - 1, y - 1)"
          >{{ stone(room.board[(y - 1) * room.size + x - 1]) }}</button>
        </template>
      </div>
      <button class="back" data-testid="back" :disabled="busy" @click="back">返回大廳</button>
    </div>

    <aside class="chat">
      <h3>房內聊天</h3>
      <ul data-testid="chat-log">
        <li v-for="(m, i) in chat" :key="i" data-testid="chat-msg"><b>{{ m.from }}：</b>{{ m.text }}</li>
      </ul>
      <form @submit.prevent="send">
        <input v-model="chatInput" data-testid="chat-input" placeholder="輸入訊息" />
        <button data-testid="chat-send">送出</button>
      </form>
    </aside>
  </main>
</template>

<style>
* { box-sizing: border-box; }
body { margin: 0; font-family: system-ui, 'Noto Sans TC', sans-serif; background: #f6f5f1; color: #222; }
header { display: flex; justify-content: space-between; padding: 10px 20px; background: #2d3a2e; color: #fff; }
main { padding: 16px 20px; }
h2 { font-size: 18px; margin: 8px 0; }
table { border-collapse: collapse; width: 100%; background: #fff; margin-bottom: 8px; }
th, td { border: 1px solid #ddd; padding: 6px 10px; text-align: left; font-size: 14px; }
th { background: #eee; }
.lobby { display: grid; grid-template-columns: 1fr 2fr 1fr; gap: 16px; align-items: start; }
.actions { display: flex; gap: 8px; margin-bottom: 8px; }
button { padding: 6px 12px; cursor: pointer; }
.error { color: #b00020; font-weight: bold; }
.empty { color: #888; text-align: center; }
.st-waiting { color: #b26a00; } .st-playing { color: #1b7f2a; font-weight: bold; } .st-finished { color: #777; }
.login form { display: flex; gap: 8px; }
.room { display: grid; grid-template-columns: auto 320px; gap: 24px; align-items: start; }
.seats { display: flex; gap: 16px; }
.seats .turn { font-weight: bold; text-decoration: underline; }
.status { font-size: 18px; font-weight: bold; }
.board { display: grid; gap: 0; --cell: 28px; background: #dcb35c; padding: 6px; width: max-content; }
.board.tictactoe { --cell: 80px; gap: 4px; background: #555; }
.cell { width: var(--cell); height: var(--cell); padding: 0; border: 1px solid #a07c3a; background: transparent; font-size: 20px; line-height: 1; }
.tictactoe .cell { background: #fff; border: none; font-size: 48px; }
.cell.black { color: #000; }
.cell.win { background: #ffe57f !important; }
.back { margin-top: 12px; }
.chat { background: #fff; border: 1px solid #ddd; padding: 10px; }
.chat ul { list-style: none; padding: 0; margin: 0 0 8px; height: 300px; overflow-y: auto; border: 1px solid #eee; }
.chat li { padding: 4px 6px; font-size: 14px; }
.chat form { display: flex; gap: 6px; }
.chat input { flex: 1; }
</style>
