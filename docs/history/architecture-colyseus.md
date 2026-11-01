# 架構草案：Colyseus 版（圍棋第一版）

> **已不採用**（2026-10-31）。目前的規格見 [spec_v1.md](../spec_v1.md)，路線見 [roadmap.md](../roadmap.md)。

> 整理日期：2026-10-30。狀態：**草案，尚未實作**。
> 依據：[設計討論紀錄](design-discussion.md)（第 2～4、8～10 節）與 `poc/colyseus` 分支的結構（`Lobby.ts`、`GameRoom.ts`、`rules.ts`、`db.ts`）。
> 流程圖中的函式是**虛擬碼**，用來說明誰呼叫誰。Colyseus 的 API 名稱（`matchMaker.createRoom`、`reserveSeatFor`、`consumeSeatReservation`、`allowReconnection`、`presence.publish` 等）以 0.18 版型別定義為準，實作時要再確認。

## 1. 架構圖

```mermaid
flowchart TB
  subgraph Browser["瀏覽器（Vue）"]
    LobbyView["大廳畫面<br/>房間列表、玩家列表、邀請彈窗"]
    GameView["對局畫面<br/>棋盤、計時、聊天、點目"]
    BoardGo["圍棋棋盤元件<br/>goban（OGS）"]
    BoardOther["其他遊戲棋盤元件<br/>五子棋、井字棋"]
    SharedRules["共用規則模組<br/>只做即時提示與預估目數"]
    GameView --> BoardGo
    GameView --> BoardOther
    GameView -.-> SharedRules
  end

  subgraph Server["Colyseus 伺服器（Node.js）"]
    Lobby["LobbyRoom：大廳管理器<br/>玩家狀態、邀請、房間列表推送"]
    Matchmaker["Colyseus matchMaker<br/>建房、保留座位"]
    subgraph GR["GameRoom：房間引擎（每桌一個）"]
      Seats["座位與斷線重連"]
      Dispatch["動作分派"]
      Proposals["提議機制<br/>點目確認、之後的悔棋與暫停"]
      Chat["房內聊天"]
      Recorder["對局紀錄"]
    end
    Registry["遊戲註冊表"]
    Defs["遊戲定義 JSON<br/>人數、可調參數、能力"]
    GoModule["圍棋規則模組<br/>包住 goban-engine"]
    OtherModules["五子棋、井字棋規則模組"]
    ClockMod["計時模組<br/>純計算、注入時間"]
  end

  DB[("PostgreSQL<br/>game_records、chat_messages")]
  KataGo["KataGo 分析服務<br/>之後再加"]

  LobbyView <-- "WebSocket" --> Lobby
  GameView <-- "WebSocket" --> GR
  Lobby --> Matchmaker
  Matchmaker --> GR
  GR -- "狀態變更（metadata）" --> Lobby
  Dispatch --> Registry
  Registry --> Defs
  Registry --> GoModule
  Registry --> OtherModules
  Dispatch --> ClockMod
  Proposals --> Dispatch
  Recorder --> DB
  Chat --> DB
  GoModule -.-> KataGo
```

原則（見設計討論第 2 節）：

- **大廳不碰棋盤**：LobbyRoom 只知道房間的遊戲類型、設定、座位與狀態。
- **規則模組不碰網路**：只做「狀態 + 動作 → 新狀態」，不知道玩家怎麼連線，也不知道時間。
- **計時模組不碰規則**：只做扣時、讀秒、判定超時，時間由房間引擎傳入。
- **伺服器是裁判**：前端的共用規則模組只用來即時提示，結果一律以伺服器為準。

## 2. 各模組的職責與主要函式（虛擬碼）

| 模組 | 職責 | 主要函式 |
| --- | --- | --- |
| LobbyRoom | 玩家上下線、玩家狀態（空閒／對局中）、邀請、房間列表推送 | `onJoin`、`onLeave`、`onMessage("invite")`、`onMessage("invite_reply")`、`expireInvite`、`setPlayerStatus` |
| GameRoom | 座位、動作分派、提議、聊天、計時器、紀錄、結束 | `onCreate`、`onJoin`、`onMessage("ready")`、`onMessage("action")`、`onMessage("chat")`、`onDeadline`、`finish`、`onLeave`、`onDispose` |
| 遊戲註冊表 | 依遊戲類型取得定義與規則模組 | `getDefinition(type)`、`getModule(type)`、`validateSettings(type, settings)` |
| 規則模組（GameModule） | 遊戲規則 | `newGame(settings, seats)`、`currentTurn(state)`、`phase(state)`、`validate(state, seat, action)`、`apply(state, seat, action)`、`result(state)`、`view(state, seat)`、`replay(settings, actions)`、`exportRecord(state)` |
| 計時模組（Clock） | 扣時、讀秒、超時 | `start(seat, now)`、`switchTo(next, now, lagMs)`、`pause(now)`、`resume(now)`、`nextDeadline()`、`check(now)`、`snapshot(now)` |
| 提議機制（Proposals） | 需要對方同意的事 | `propose(from, type, params)`、`respond(id, answer)`、`cancelAll(type)` |
| 對局紀錄（Recorder） | 保存事件與結果 | `begin(meta)`、`append(event)`、`build(result)`、`save(record)` |

規則模組比 PoC 多了 `phase`、`view`、動作類型與參與座位，這些是加圍棋必需的（見設計討論第 2 節與「現有介面草案的缺口」的討論）。

## 3. 流程：邀請對局到結束對局

情境：Alice 邀請 Bob 下一盤 19 路圍棋（Alice 執黑、貼目 6.5、日式讀秒）。為了好讀，拆成四段。

### 3.1 邀請到開局

```mermaid
sequenceDiagram
  autonumber
  actor A as Alice（瀏覽器）
  actor B as Bob（瀏覽器）
  participant L as LobbyRoom（大廳）
  participant MM as matchMaker
  participant G as GameRoom（房間引擎）
  participant RM as 圍棋規則模組
  participant C as 計時模組

  A->>L: send("invite", to=Bob, settings)
  L->>L: canInvite(Alice, Bob)<br/>Bob 在線、空閒、開放邀請、沒有待回覆的邀請
  L->>L: registry.validateSettings("go", settings)
  L->>L: invites.create(from=Alice, to=Bob, settings, expiresAt=now+60s)
  L->>L: clock.setTimeout(expireInvite, 60s)
  L-->>B: send("invite_received", invite)
  L-->>A: send("invite_sent", invite)

  alt Bob 接受
    B->>L: send("invite_reply", inviteId, accept)
    L->>L: invites.accept(inviteId)
    L->>MM: createRoom("game", gameType="go", settings, seats=[Alice, Bob], private)
    MM->>G: onCreate(options)
    G->>G: def = registry.getDefinition("go")<br/>rules = registry.getModule("go")
    G->>G: setPrivate(true)，setMetadata(status="waiting")
    MM-->>L: room
    L->>MM: reserveSeatFor(room, Alice)
    L->>MM: reserveSeatFor(room, Bob)
    L-->>A: send("seat_reserved", reservationA)
    L-->>B: send("seat_reserved", reservationB)
    L->>L: setPlayerStatus([Alice, Bob], "playing")<br/>推送玩家列表變化
  else Bob 拒絕
    B->>L: send("invite_reply", inviteId, decline)
    L-->>A: send("invite_declined")
  else Bob 改條件（還價）
    B->>L: send("invite", to=Alice, newSettings)
    L->>L: 自動拒絕 Alice 的邀請，改成 Bob 向 Alice 提出
    L-->>A: send("invite_received", newInvite)
  else 逾時或 Alice 取消
    L->>L: expireInvite(inviteId)
    L-->>A: send("invite_expired")
    L-->>B: send("invite_expired")
  end

  A->>G: consumeSeatReservation(reservationA)
  G->>G: onJoin(Alice)：確認 Alice 是保留座位的人
  B->>G: consumeSeatReservation(reservationB)
  G->>G: onJoin(Bob)
  A->>G: send("ready")
  B->>G: send("ready")
  Note over G: 雙方都 ready 才開始，避免第一個狀態比前端 handler 早到（PoC 遇過）
  G->>RM: newGame(settings, seats=[Alice 黑, Bob 白])
  RM-->>G: state（phase="play"，輪到黑）
  G->>C: start(seat=黑, now)
  G->>G: scheduleDeadline(clock.nextDeadline())
  G->>G: recorder.begin(meta)，setMetadata(status="playing")
  G-->>A: send("state", rules.view(state, Alice) + clock.snapshot(now))
  G-->>B: send("state", rules.view(state, Bob) + clock.snapshot(now))
```

### 3.2 對局中：落子與超時

```mermaid
sequenceDiagram
  autonumber
  actor A as Alice（黑）
  actor B as Bob（白）
  participant G as GameRoom（房間引擎）
  participant RM as 圍棋規則模組
  participant C as 計時模組
  participant R as Recorder

  A->>G: send("action", type="play", x=3, y=3)
  G->>G: seat = seatOf(Alice)，確認對局進行中
  G->>RM: validate(state, seat, action)
  alt 不合法（不是你的回合、有子、自殺、打劫）
    RM-->>G: error
    G-->>A: send("error", message)
  else 合法
    RM-->>G: ok
    G->>RM: apply(state, seat, action)
    RM-->>G: newState（含提子）
    G->>C: switchTo(next=白, now, lagMs)
    C-->>G: usedMs、events（例如進入讀秒）
    G->>R: append(action, seat, usedMs)
    G->>G: scheduleDeadline(clock.nextDeadline())
    G-->>A: send("state", view + 時鐘)
    G-->>B: send("state", view + 時鐘)
    G->>RM: result(newState)
    RM-->>G: ongoing
  end

  A->>G: send("chat", text)
  G-->>B: send("chat", Alice, text)
  G->>R: append(chat)

  opt 超時（計時器觸發，沒有人落子）
    G->>C: check(now)
    C-->>G: events = [timeout(白)]
    G->>G: finish(result="黑勝（超時）")
  end

  opt 斷線
    B--xG: 連線中斷
    G->>G: onLeave(Bob, consented=false)<br/>allowReconnection(Bob)，時鐘照走
    B->>G: 重新連線
    G-->>B: send("state", 目前畫面 + 時鐘)
  end
```

### 3.3 終局：pass、點目、結算

```mermaid
sequenceDiagram
  autonumber
  actor A as Alice（黑）
  actor B as Bob（白）
  participant G as GameRoom（房間引擎）
  participant RM as 圍棋規則模組
  participant C as 計時模組
  participant P as 提議機制

  B->>G: send("action", type="pass")
  G->>RM: validate、apply
  A->>G: send("action", type="pass")
  G->>RM: validate、apply
  RM-->>G: newState（phase="stone_removal"）
  G->>C: pause(now)
  G->>G: 點目時限計時器（例如 3 分鐘）
  G-->>A: send("state", phase=點目, 預估目數)
  G-->>B: send("state", phase=點目, 預估目數)

  loop 標記死子
    A->>G: send("action", type="toggle_dead", x, y)
    G->>RM: apply(state, seat, action)
    RM-->>G: newState（死子標記、預估目數）
    G->>P: cancelAll("done")<br/>標記改變，先前的「完成」作廢
    G-->>A: send("state")
    G-->>B: send("state")
  end

  A->>G: send("action", type="done")
  G->>P: propose(Alice, "done", marks=rules.marksHash(state))
  G-->>B: send("proposal", Alice 已按完成)
  B->>G: send("action", type="done")
  G->>P: propose(Bob, "done", marks=同一份)
  P-->>G: 雙方對同一份標記都同意
  G->>RM: apply(state, system, type="finalize")
  RM-->>G: newState（phase="finished"）
  G->>RM: result(newState)
  RM-->>G: 黑勝 3.5 目（數子法，含貼目）
  G->>G: finish(result)

  alt 點目逾時，或一方中途認輸
    G->>G: finish(依規則判定的結果)
  end
```

### 3.4 結束：存紀錄、返回大廳

```mermaid
sequenceDiagram
  autonumber
  actor A as Alice
  actor B as Bob
  participant G as GameRoom（房間引擎）
  participant RM as 圍棋規則模組
  participant R as Recorder
  participant DB as PostgreSQL
  participant L as LobbyRoom（大廳）

  Note over G: finish(result)
  G->>G: clock.stop()，取消所有計時器，lock()
  G->>RM: exportRecord(state)
  RM-->>G: 棋步、結果字串（例如 B+3.5），之後可轉 SGF
  G->>R: build(result)
  R->>DB: INSERT game_records（game_id、game_type、players、settings、moves、result、started_at、ended_at、rated=false）
  G->>G: setMetadata(status="finished")
  G-->>A: send("game_over", result)
  G-->>B: send("game_over", result)
  G-->>L: 房間狀態變更（realtime listing 推送給大廳裡的人）

  A->>G: leave()
  B->>G: leave()
  G->>G: onLeave × 2，房間清空
  G->>G: onDispose()（autoDispose）
  G-->>L: 房間從列表移除
  A->>L: 回到大廳
  B->>L: 回到大廳
  L->>L: setPlayerStatus([Alice, Bob], "idle")<br/>推送玩家列表變化
```

## 4. 待確認

- Colyseus 0.18 的實際 API：`matchMaker.createRoom`、`reserveSeatFor`、`consumeSeatReservation`、`setPrivate`、`allowReconnection`、`presence.publish` 的名稱與參數。
- 私人房間（邀局建立的房間）要不要出現在大廳的房間列表（顯示為「進行中、不可加入」，或完全隱藏）。
- 玩家狀態由誰維護：LobbyRoom 在建房時標成「對局中」，玩家回大廳時改回「空閒」；斷線未回大廳的情況要定規則。
- `goban-engine` 的資料格式怎麼對應到 `validate`、`apply`、`view`；「完成」的協商是由提議機制處理，還是直接用 `goban-engine` 內建的 stone removal 流程。
- 觀戰者的加入方式與講評頻道（設計討論第 5.5、6 節），本草案還沒畫。

## 附錄：與規格 v1 的差異

| 項目 | 本草案（Colyseus） | 規格 v1 |
| --- | --- | --- |
| 對局在哪裡跑 | Colyseus 的 GameRoom（Node） | 房間服務（Go），每個房間一個 goroutine |
| 服務數量 | 1 個 Node 程序 | 3 個獨立服務：大廳服務（Go）、房間服務（Go）、規則服務池（Node），服務之間用 gRPC |
| 建房與座位 | `matchMaker.createRoom` + `reserveSeatFor` | 大廳以 gRPC 呼叫 `CreateRoom`、`ReserveSeat`；座位由房間分配 |
| 玩家連線 | 一個 Colyseus client，先連大廳再進房 | 兩條 WebSocket：大廳、房間；入場券 + 重連憑證 |
| 規則在哪裡跑 | 房間程序內直接呼叫 `goban-engine` | 以 gRPC 呼叫規則服務，每次帶完整動作紀錄 |
| 計時器 | Colyseus 的 `clock.setTimeout` | Go `time.Timer`，觸發時送事件進房間；依收到時間判超時 |
| 房間列表 | GameRoom 的 metadata 自動推送 | 大廳依房間事件維護列表項並推送；所有對局公開可觀戰 |
| 觀戰 | 未設計 | 觀戰入場券、講評只給觀戰者、每條連線獨立送出佇列 |
| 狀態同步 | 同一程序，不需要 | 房間服務 → 大廳服務事件（帶序號）+ 定期對帳 |
| 重啟影響 | 程序重啟，大廳和對局一起中斷 | 大廳服務、房間服務可分別重啟；停機期間不扣時 |
| 前端先擺子 | 未設計 | 對局階段先擺子；房間發版本號對帳 |
| 終局方式 | 雙方手動標記死子，都按完成才定案 | 規則服務自動提出死子判斷，雙方可手動切換死子後同意；談不攏才恢復對局 |
| 到場時限 | 未設計 | 有 |
| 用詞 | 房間引擎 | 大廳、房間、規則（見第 0 節） |
| 遊戲範圍 | 圍棋、五子棋、井字棋 | 圍棋、五子棋（井字棋之後再補） |
