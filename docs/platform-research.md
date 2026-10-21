# 線上棋類平台研究：大廳管理器與遊戲邏輯模組

> 整理日期：2026-10-21（資料查詢於 2026-10-20）。
> 內容來自網路搜尋與各專案首頁／文件，除特別註明外**沒有實際執行過程式碼**。採用前請自行確認授權、維護狀況與版本。

## 1. 目標與架構概念

目標是做一個類似野狐的線上棋類平台，先做五子棋，之後可擴充圍棋等其他棋類。

系統分成兩層：

- **大廳管理器**：獨立的一層，管理線上玩家、房間（桌子）列表、配對、建立與加入，**不碰任何遊戲規則**。
- **遊戲邏輯模組**：每種遊戲（五子棋、圍棋…）是各自獨立的模組，只負責規則：落子是否合法、輪到誰、誰贏。大廳透過統一介面呼叫它。

好處：新增一種遊戲只需要新增一個規則模組，大廳不用改。

### 1.1 大廳管理器的職責

- 線上玩家狀態（presence）
- 房間／桌子列表（可依條件過濾）
- 配對：加入碼、快速配對
- 建立與加入房間（含開房設定，例如先後手）
- 斷線重連：座位綁使用者 ID，而不是綁連線
- 對局紀錄保存（以棋步清單為準）
- 使用者登入與 token 驗證
- 之後再做：積分天梯、聊天、觀戰

### 1.2 遊戲邏輯模組介面（草案，非研究結論）

```text
GameModule
  id                                   // "gomoku"、"go"…
  newGame(options)         -> state    // options：棋盤大小、規則、先後手
  currentTurn(state)       -> player
  validateMove(state, player, move) -> ok | error
  applyMove(state, player, move)    -> state
  result(state)            -> ongoing | win(player) | draw
  exportRecord(state)      -> 棋譜（例如 SGF）
```

設計原則：

- 純邏輯，不碰網路與資料庫
- 狀態可以由「棋步清單」完整重建（斷線重連、重啟還原、棋譜匯出都靠這個）
- AI 引擎是另一個獨立介面（五子棋可用 Piskvork 協議接 Rapfi 等引擎），與遊戲規則模組分開

## 2. 候選底座比較

| 方案 | 類型／語言 | 授權 | 大廳與配對 | 帳號、積分、聊天 |
| --- | --- | --- | --- | --- |
| Nakama | 完整後端平台／Go（可用 Go、TypeScript、Lua 寫擴充） | Apache-2.0 | 配對器；對局列表可用 label 與查詢過濾 | 內建：社群／email／裝置 ID 登入、好友、聊天、排行榜、錦標賽 |
| Colyseus | 房間框架／Node.js、TypeScript | MIT | 內建 LobbyRoom（即時房間列表與過濾）、Queue Room、配對 API | 需自建（有 onAuth 驗證 token 的機制） |
| boardgame.io | 回合制遊戲引擎／JavaScript | MIT | Lobby REST API；React 大廳元件 | 需自建 |
| 分叉 lila（Lichess） | 完整網站／Scala 3 | AGPL-3.0 | 大廳、錦標賽、同時對弈、戰隊、論壇都現成 | 現成，但要自己加五子棋規則模組與介面 |
| 自己寫 | 任意（例如 Go + WebSocket） | — | 自己做 | 自己做 |

### 維護狀況（GitHub 首頁，2026-10 查詢）

| 專案 | Stars | Open issues | Open PRs | Commits |
| --- | --- | --- | --- | --- |
| Nakama | 13.3k | 91 | 37 | 1,837 |
| Colyseus | 7.4k | 14 | 4 | 2,247 |
| boardgame.io | 12.5k | 54 | 14 | 1,913 |

補充：

- **Colyseus**：文件已更新到 0.17；有公開的 1.0 路線圖；靠 Colyseus Cloud 訂閱與贊助維持，核心開發看起來集中在創辦人一人。
- **boardgame.io**：較舊的一份統計快照顯示最後 push 為 2024-09、活躍維護者 0、PR 平均 141 天才處理；現在的 issue 數少於當時的 108 個，推測之後有人回來清理，但這是推論。
- **Nakama**：背後有商業公司（Heroic Labs）。
- 三者都沒有查到最近一次 commit 的確切日期與 issue 回覆速度，請看各專案 GitHub 的 Insights → Pulse。

## 3. 四個關鍵能力對照

| 能力 | Nakama | Colyseus | boardgame.io | 自己寫 |
| --- | --- | --- | --- | --- |
| 建立房間 | 註冊對局處理器（match handler），常用 RPC 建立對局 | 自訂 Room 類別並註冊，房間按需建立、空了可自動銷毀 | Lobby API 的 create 與 join；可發放憑證 | 一個房間一個物件（例如 Go 的 struct 加 goroutine） |
| 紀錄保存 | 內建儲存與 Postgres／CockroachDB | 需自行存資料庫；devMode 的快取只能用在開發 | 預設在記憶體，可接 Mongo、PostgreSQL、Firestore | 每步落子寫入資料庫，結束時存棋譜 |
| 斷線重連 | 在對局處理器的加入／離開邏輯處理；社群範例有保留重連時間 | onDrop 中呼叫 allowReconnection(秒數)；0.17 起有內建自動重連 | 客戶端重連時重新同步，玩家用憑證回原座位 | 座位綁使用者 ID，重連時送完整棋步清單 |
| 綁定使用者 token／OAuth | 內建社群帳號、email、裝置 ID 登入 | onAuth 在加入前驗證 token；官方範例有 Firebase 驗證 | 可自訂 generateCredentials（可非同步呼叫第三方）與 authenticateCredentials | WebSocket 連線後第一個訊息送 token，不要放在網址上 |

## 4. 各方案已知限制

### Nakama

- **開源版只有單節點**；叢集管理（高可用、自動故障轉移）是 Enterprise 版功能，需商業授權。單節點可跑數百到數千個對局。
- **TypeScript／JavaScript 執行環境**：跑在 goja 虛擬機上，只支援 ES5、不支援多執行緒，沒有 Node 的功能，很多 npm 套件不能用，連 setTimeout 都沒有。
- **Go 與 TypeScript 不能互相呼叫函式**；Go 需編譯成共享函式庫（.so）載入。
- 對局處理是以 **tick** 為單位，回合制官方建議用最低 tick 頻率 1，落子在下一個 tick 才被處理，頻率太低會有延遲。
- 需要 CockroachDB 或相容 Postgres 的資料庫。
- 使用者意見（產品評論網站）：照官方結構做會很快，非常客製的功能較困難；排行榜不易客製、文件多半只講成功流程。僅供參考。
- 文件中有些功能標示為 Enterprise 限定，**尚未確認是哪些**。

### Colyseus

- 重啟後要還原房間，官方論壇的說法是自己把必要狀態存進資料庫再載入；devMode 的快取明確標示不能用在正式環境。
- 帳號、積分、聊天紀錄都要自己做。
- 核心維護者集中的風險。

### boardgame.io

- 維護活躍度是三者中最低的。
- 大廳元件是 React 版，Vue 要自己做畫面（API 可直接用）。

### 分叉 lila

- 技術棧重：Scala 3、Play 框架、MongoDB、獨立的 WebSocket 伺服器（lila-ws）加 Redis，另有分析引擎的分散機制。
- AGPL：修改後提供網路服務，要對使用者公開原始碼。
- **沒有查到五子棋分支。** 已知的真實分支有 Lishogi（將棋）、Lidraughts（國際跳棋）、PlayStrategy（多種棋類，2023 年的清單有西洋棋、將棋、象棋、跳棋、黑白棋、圍棋等，沒有五子棋，清單可能已過時）。
- 分叉其他遊戲的做法是把規則寫成獨立模組、棋盤介面元件改寫成該遊戲版本。

## 5. 可用的開源零件

### 5.1 五子棋規則

| 專案 | 語言 | 說明 |
| --- | --- | --- |
| renju_forbid | Go（MIT） | 三三、四四、長連禁手判定；**輸入是 SGF 字串**，棋步清單要先轉 SGF |
| omok | Python | 預設 Renju 規則（黑棋三三、四四、長連禁手，只有剛好五連才贏），也支援 freestyle |
| renju | Python（MIT） | 棋盤與裁判規則、SGF 支援 |
| pygomo-lib | Python（MIT） | 用 Gomocup 協議與引擎溝通；含支援 Renju 禁手的棋盤實作 |
| mnkgame | Go | 通用 m,n,k 棋類，15×15 連五即自由式五子棋；沒有禁手，維護狀況未確認 |

建議：freestyle 勝負判定自己寫（從落子點往四個方向數連續同色，幾十行）；禁手不要從頭寫，請 AI 直接寫完整實作會出錯，尤其是四四；用 Python 的 omok 或 renju 產生測試局面，當作對照。

### 5.2 五子棋 AI 引擎

- **Rapfi**：頂尖的開源引擎，支援 Gomoku 與 Renju，無圖形介面，透過 Piskvork 協議以子程序溝通；也有用 WebAssembly 的網頁版（Gomoku Calculator）。
- 學習用：alphazero_gomoku（自我對弈訓練）；Minimax 範例如 Slijeff/game-ai（React 前端加 Flask 後端）、luunam/gomoku-react（可抽換 bot）。
- Piskvork 協議常用指令：`START`、`BEGIN`、`TURN x,y`、`BOARD … DONE`、`INFO timeout_turn`、`END`。座標為 0-based。

### 5.3 等級分

- Go 套件：treepeck/glicko（Glicko-2）、watson-sam/elo（MIT）、artasparks/gorating（含 Glicko、USCF）。
- 連珠專業評分網站使用 WHR 演算法（wind23/whr_renju，Python，用歷史棋譜離線計算，不適合即時更新）。

### 5.4 棋譜格式

- SGF 的 `GM[4]` 是五子棋與連珠，棋盤大小用 `SZ[15]`，每手為 `B[xy]` 或 `W[xy]`。
- 不是所有 SGF 程式都認得 `GM[4]`。
- 內部只存棋步清單，要保存或分享時再轉成 SGF。

## 6. 可參考的範例

- **Colyseus**：demo-tic-tac-toe（官方回合制雙人範例，MIT，Colyseus 0.16）、Turn Based Tanks（非同步回合制，大廳依房間 metadata 過濾；使用舊版 0.14）、colyseus-examples。
- **Nakama**：XOXO 井字棋教學（官方，PhaserJS）、專案範本（nakama-project-template）、社群的井字棋（含重連保留時間、每步 30 秒限時）、官方部落格的「對局中接上 AI 對手」文章。
- **TableCraft**：開源桌遊平台，13 款遊戲含五子棋，有大廳與觀戰；授權未確認。
- **OGS**：online-go.com 倉庫是網頁客戶端原始碼（AGPL v3），後端是否開源未確認。

## 7. 風險與注意事項

- **作弊**：Rapfi 等強引擎都是開源的，玩家很容易拿來輔助，平台需要偵測機制，需長期投入。
- **法規**：若面向中國大陸的公開使用者，帳號、聊天內容與網站備案可能有法規要求，需請專業人士確認。
- **授權**：AGPL（lila 系）有公開原始碼義務；Nakama 的 Enterprise 版為商業授權。
- **資料時效**：以上查詢於 2026-10-20，專案狀態會變動。

## 8. 未驗證／待研究

- 原先截圖提到的 `GomokuOnline/gomoku-online`（lila 分支）、`yixuan-wang/gomoku-server`、Govariants.com／go.kahv.io，**搜尋都沒有找到對應專案**；`jeff-hykin/gomoku` 未搜尋。
- Rapfi 能不能在 Nakama 的 Go 執行環境裡以子程序方式穩定運作：**未驗證，需實測**。
- Nakama 文件中標示 Enterprise 限定的功能清單：未確認。
- 各專案最近 commit 日期與 issue 回覆速度：未取得。
- **圍棋規則的開源實作**（提子、打劫、數子／數目）：**完全沒有研究**。若平台要做圍棋，這是下一步。

## 9. 建議的下一步

1. 用 Nakama 做一到兩天的小實驗：Docker 啟動，寫一個最小的五子棋對局處理器，開兩個瀏覽器測落子、斷線重連與排行榜。
2. 實驗中驗證 Rapfi 在 Go 執行環境的整合。
3. 同時把「遊戲邏輯模組介面」定稿，確保規則模組不依賴任何底座，這樣之後換底座時規則程式碼可以保留。
4. 若實驗不順，備案是 Colyseus（要自建帳號與積分）或自己用 Go 寫大廳。

## 附錄 A：來源專案（五子棋對戰）已定決策

- 三個主畫面按鈕：AI 對戰、人人對戰、加入房間
- 開房時選先手或後手；人人對戰開房後停在加入碼畫面等待對手
- 第一版只做 freestyle；禁手（黑棋觸犯即判負）之後再加，不提供悔棋
- 先把 API 寫好並測過，再做前端畫面
- 伺服器端判定落子合法與勝負；引擎介面可替換（內建 Minimax、Piskvork 外部引擎、遠端服務）

## 來源

### Nakama
- [Nakama GitHub](https://github.com/heroiclabs/nakama)
- [Go 執行環境範例（對局處理器）](https://heroiclabs.com/docs/nakama/server-framework/go-runtime/code-samples/)
- [權威多人遊戲](https://heroiclabs.com/docs/zh/nakama/concepts/multiplayer/authoritative)
- [多人遊戲引擎概念](https://heroiclabs.com/docs/nakama/concepts/multiplayer/)
- [架構概覽](https://heroiclabs.com/docs/nakama/getting-started/architecture/llm.md)
- [Nakama Enterprise](https://heroiclabs.com/enterprise/)
- [TypeScript 執行環境](https://heroiclabs.com/docs/nakama/server-framework/typescript-runtime/)
- [伺服器框架簡介](https://heroiclabs.com/docs/nakama/server-framework/introduction/)
- [論壇：Go 與 JavaScript 比較](https://forum.heroiclabs.com/t/go-vs-javascript-runtime/5950)
- [論壇：setTimeout 問題](https://forum.heroiclabs.com/t/what-do-i-use-instead-of-settimeout-in-the-js-runtime/5265)
- [XOXO 井字棋教學](https://heroiclabs.com/docs/nakama/tutorials/javascript/xoxo/)
- [社群井字棋範例（重連與限時）](https://github.com/kshru9/tic-tac-toe-lila)
- [Nakama 接 AI 對手](https://heroiclabs.com/blog/nakama-ml-integration/)
- [Defold 與 Nakama 的 XOXO](https://github.com/defold/game-xoxo-nakama-client)

### Colyseus
- [Colyseus GitHub](https://github.com/colyseus/colyseus)
- [LobbyRoom](https://docs.colyseus.io/room/built-in/lobby)
- [房間生命週期](https://docs.colyseus.io/room/lifecycle)
- [重連](https://docs.colyseus.io/room/reconnection)
- [devMode](https://docs.colyseus.io/server/devmode)
- [論壇：重啟後還原房間](https://discuss.colyseus.io/post/652)
- [demo-tic-tac-toe](https://github.com/colyseus/demo-tic-tac-toe)
- [Turn Based Tanks](https://docs.colyseus.io/learn/tutorial/unity/turn-based-tanks)
- [範例總覽](https://docs.colyseus.io/learn/examples)

### boardgame.io
- [boardgame.io GitHub](https://github.com/boardgameio/boardgame.io)
- [Lobby API](https://github.com/boardgameio/boardgame.io/blob/21f23277a42b62290b33661cc95caf5e9b537664/docs/documentation/api/Lobby.md)
- [儲存（預設記憶體、Mongo）](https://github.com/nicolodavis/boardgame.io/commit/d999038415ca4a91c6142a0af1c71b3010611c2d)
- [bgio-postgres](https://www.npmjs.com/package/bgio-postgres)
- [自訂憑證](https://github.com/nicolodavis/boardgame.io/commit/4d33faa970f536a717df9777fe85c12e00b6e27c)
- [統計快照（ecosyste.ms）](https://opencollective.ecosyste.ms/projects/7858)

### lila 與分支
- [lila 說明](https://github.com/NSK07/lila/blob/master/README.md)
- [Lishogi](https://github.com/SriMethan/lishogi)
- [Lidraughts](https://github.com/roepstoep/lidraughts)
- [PlayStrategy](https://github.com/m-dinhhoangviet/lila-1)
- [PlayStrategy 支援遊戲](https://chessvariants.org/link/playstrategy)
- [Lichess 開發環境](https://github.com/lichess-org/lila/wiki/Lichess-Development-Onboarding)

### 其他平台
- [TableCraft](https://github.com/Clarence-G/tablecraft)
- [OGS 網頁客戶端](https://github.com/online-go/online-go.com)

### 五子棋規則、引擎與評分
- [renju_forbid](https://github.com/realjustice/renju_forbid)
- [omok](https://pypi.org/project/omok/0.1.0/)
- [renju](https://pypi.org/project/renju/)
- [pygomo-lib](https://pypi.org/project/pygomo-lib/)
- [mnkgame](https://pkg.go.dev/github.com/rsned/games/mnkgame)
- [Rapfi](https://github.com/dhbloo/rapfi)
- [Gomoku Calculator](https://github.com/lejunzhu/gomocalc.github.io)
- [alphazero_gomoku](https://github.com/hengqujushi/alphazero_gomoku)
- [Slijeff/game-ai](https://github.com/Slijeff/game-ai)
- [luunam/gomoku-react](https://github.com/luunam/gomoku-react)
- [禁手實作心得](https://gallery.selfboot.cn/en/blog/gomoku)
- [treepeck/glicko](https://pkg.go.dev/github.com/treepeck/glicko)
- [watson-sam/elo](https://github.com/watson-sam/elo)
- [gorating](https://www.github.com/artasparks/gorating)
- [whr_renju](https://github.com/wind23/whr_renju)
