# boardgame.io PoC 測試結果

- 測試日期：2026-10-22
- 版本：boardgame.io 0.50.2、bgio-postgres 1.0.16、Postgres 16、Vue 3.5、Vite 7.3、Playwright 1.64（Chromium headless）、Node 20
- 執行方式：`./scripts/test.sh`（建置 → `docker compose down -v` 清空資料庫 → 啟動 → E2E → 資料庫驗證）。單獨執行與三框架平行執行都完整通過。

## 結果摘要

E2E 19 項檢查、資料庫 14 項檢查全部通過。

| 檢查項目 | 結果 | 說明 |
| --- | --- | --- |
| 大廳列表 | ✅ | 單一「主大廳」，顯示線上人數 |
| 房間列表（即時更新） | ✅（輪詢） | Lobby API `listMatches` 每秒輪詢；沒有推送機制 |
| 玩家列表（有／無） | ✅ 有（自行實作） | 框架沒有線上狀態；自己加了 `/presence` 心跳路由（每 2 秒，5 秒未更新即視為離線） |
| 兩種遊戲都能建房與對戰 | ✅ | 兩個 bgio Game 定義共用同一份規則模組 |
| 兩場同時進行互不干擾 | ✅ | |
| 已滿房間拒絕加入 | ✅ | Lobby API 回 409 `Match … reached maximum number of players (2)` |
| 房內聊天與隔離 | ✅ 內建 | `sendChatMessage`／`chatMessages`，只在同一場對局內轉發；**不會保存** |
| 對局紀錄寫入資料庫 | ✅ | bgio-postgres 自動把每場對局的 metadata 與最新 state 存進 `Games` 表；DB 驗證直接從 state 取棋步 |
| 返回大廳 | ✅（有條件） | 已結束的房間保留並標示「已結束」，不會移除（原因見下方「重要發現」1） |
| 非法落子被拒絕 | ✅ | 伺服器與客戶端都會拒絕；畫面上的錯誤訊息來自客戶端（見「重要發現」2） |

N/A 項目：無。

落子延遲（點擊到畫面出現棋子，在瀏覽器內每個畫面幀檢查，14 步）：下棋者 31 ms（13～48）、對手 41 ms（16～50）。下棋者畫面是客戶端先自行算出結果（樂觀更新），不等伺服器。

## 重要發現

1. **所有玩家都呼叫 `leave` 後，比賽會從資料庫刪除**（`db.wipe`）。Lobby API 的設計是「沒人就刪掉」，連同已結束的對局紀錄一起刪除。PoC 的作法是：對局結束後返回大廳**不呼叫 `leave`**，只斷開連線，所以已結束的房間會一直留在列表上（標示「已結束」）。正式版需要另外的保存機制（例如對局結束時另存一份紀錄、或改寫 storage 的 `wipe`）與清理已結束房間的排程。
2. **伺服器拒絕落子時不會告訴客戶端原因**。bgio 把錯誤放在 `transients`，伺服器端直接丟掉；客戶端會用同一份 reducer 先跑一次，錯誤（`action/inactive_player`、`action/invalid_move`）只存在客戶端本地狀態，而且下一次通知時就被清掉。畫面的錯誤訊息是 adapter 自己從本地狀態抓、再用規則模組重算原因（例如「這格已經有棋子」）。如果客戶端被竄改，伺服器仍會拒絕，只是不回傳原因。
3. **`isConnected` 不可靠**：四位玩家幾乎同時離開後，資料庫中 Player1 仍是 `isConnected: true`、Player3 的欄位直接消失。推測是斷線處理對 metadata 做「讀出 → 修改 → 寫回」時互相覆蓋（未深入確認）。所以 adapter 不用它判斷誰還在房間裡。
4. **bgio 沒有「等人到齊」的概念**：建立對局後馬上就能落子。加了一個 `wait` 階段，兩位玩家連線後各自呼叫 `sit`，兩人都坐下才進入 `play` 階段（並記錄 `startedAt`）。
5. 座位憑證（`credentials`）以明文存在 `Games.players` 欄位，DB 驗證的查詢特意只取名字。
6. 相依套件有已知漏洞：`npm audit` 顯示 13 個（6 high、5 moderate、2 low），部分來自已停止更新的套件。boardgame.io 本身最後一版是 0.50.2。

## 實作方式與自行實作的部分

- **伺服器**：bgio 內建 Koa 伺服器，在 8903 同時提供 Lobby API、socket.io 與 Vue 靜態檔（`koa-static`）；只有 Postgres 跑在 Docker。
- **規則模組** `server/src/rules.ts`：與其他分支相同，6 個單元測試；`server/src/games.ts` 把它包成 bgio Game，2 個用 bgio 本地 client 跑的測試。Game 定義由伺服器與 Vue 客戶端共用（bgio 兩邊都會跑 move）。
- **大廳畫面**：bgio 的大廳元件是 React 版，Vue 前端直接呼叫 `LobbyClient`（REST）自己做，約 100 行 adapter。
- **玩家列表**：自訂 `/presence` 路由（約 15 行），只在單一程序有效。
- **前端狀態處理**：bgio 每次 dispatch 都會通知訂閱者，包含清除錯誤的那一次，也會在訂閱函式裡重入（在 callback 裡呼叫 `moves.sit()`）。adapter 需要自己判斷「畫面上看得到的東西有沒有變」才更新，這部分花了最多除錯時間。

## 遇到的問題

1. 本地 multiplayer 測試時，`validateSetupData` 要求 `host` 會讓沒有 setupData 的本地對局建立失敗且不報錯（state 一直是 null）。已移除這個驗證。
2. 狀態去重的 key 只用 `_stateID` 時，客戶端樂觀更新與伺服器權威狀態有相同的 `_stateID` 但 phase 不同（客戶端不會自行切換 phase），導致漏掉進入 `play` 的更新。key 改為包含 phase、目前玩家、是否結束與座位。
3. 執行腳本最初用 `npx tsx` 啟動伺服器，結束時殘留 node 程序佔住 8903，下一輪連到舊程序（而它連的資料庫已被清空）而出現 HTTP 500。已改為 `exec node --import tsx`。
4. 測試期間 npm registry 的安全稽核端點卡住，導致 `npm ci` 不會結束；改為加上 `--no-audit`。

## 粗估實作時間

以 AI 協助開發計（前端畫面、E2E 腳本沿用 Nakama 分支）：Game 定義與測試約 20 分鐘、伺服器約 10 分鐘、前端 adapter 約 20 分鐘、除錯（錯誤訊息、狀態去重、重入、isConnected）約 40 分鐘。人工開發預估 1.5～2 天，主要成本在理解 bgio 的客戶端狀態流程與補上框架沒有的部分。
