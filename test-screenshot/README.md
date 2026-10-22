# Colyseus PoC 測試結果

- 測試日期：2026-10-22
- 版本：@colyseus/core 0.18.21、@colyseus/ws-transport 0.18.4、@colyseus/sdk 0.18.5、Postgres 16、Vue 3.5、Vite 7.3、Playwright 1.64（Chromium headless）、Node 20
- 執行方式：`./scripts/test.sh`（建置 → `docker compose down -v` 清空資料庫 → 啟動 → E2E → 資料庫驗證）。連續兩次完整執行皆通過。

## 結果摘要

E2E 19 項檢查、資料庫 14 項檢查全部通過。

| 檢查項目 | 結果 | 說明 |
| --- | --- | --- |
| 大廳列表 | ✅ | 單一「主大廳」，顯示線上人數 |
| 房間列表（即時更新） | ✅ 推送 | 內建 `LobbyRoom` 加上 `enableRealtimeListing()`，房間 metadata 改變時伺服器主動推送 `+`／`-` |
| 玩家列表（有／無） | ✅ 有（自行實作） | 繼承 `LobbyRoom`，自己記錄連線到大廳的玩家並廣播 `players` |
| 兩種遊戲都能建房與對戰 | ✅ | 同一個 `GameRoom` 類別，以 `gameType` 選規則 |
| 兩場同時進行互不干擾 | ✅ | 每個房間是獨立的 Room 實例 |
| 已滿房間拒絕加入 | ✅ | `maxClients = 2`，滿了自動鎖房；錯誤原文是 `room "…" is locked`，前端轉成「房間已滿」 |
| 房內聊天與隔離 | ✅（自行實作） | 房間訊息 `chat` 只廣播給同房玩家；紀錄自己寫入 `chat_messages` 表 |
| 對局紀錄寫入資料庫 | ✅（自行實作） | 對局結束時用 `pg` 寫入自建的 `game_records` 表 |
| 返回大廳 | ✅ | 兩人都離開後房間自動銷毀（autoDispose），大廳立即收到 `-` 並移除 |
| 非法落子被拒絕 | ✅ | 不是自己回合、落在已有棋子的格子都由伺服器拒絕並回傳錯誤訊息 |

N/A 項目：無。

落子延遲：平均 115 ms（最小 112、最大 116，14 個樣本）。E2E 每 100 ms 檢查一次畫面，所以實際延遲大約只有 10～20 ms。Colyseus 的訊息是收到就處理，沒有 tick 延遲。

## 實作方式與自行實作的部分

- **伺服器**：Node 直接用 `tsx` 執行 TypeScript；只有 Postgres 跑在 Docker（`docker compose -p poc-colyseus`），伺服器在 8902 同時提供 WebSocket、配對 API 與 Vue 靜態檔（透過 `express` 選項）。
- **規則模組** `server/src/rules.ts`：與 Nakama 分支相同的邏輯，改成 ES module 匯出，6 個單元測試。
- **遊戲狀態用一般訊息傳送，沒有用 Schema 狀態同步**：為了讓三個分支前端介面一致。正式產品可改用 Schema，取得差量同步與自動重連後的狀態還原。
- **玩家列表**：Colyseus 沒有線上玩家清單，自己在大廳房間裡記錄（約 30 行）。只有單一程序有效；多程序部署時要改用 Presence（Redis）共享。
- **資料庫**：Colyseus 完全不管保存，自己用 `pg` 建表與寫入（約 50 行）。
- **聊天**：沒有內建聊天，用房間訊息實作，隔離是自然得到的（訊息只送給同房的 client）。
- **斷線重連**：`onDrop` 中呼叫 `allowReconnection(client, 20)`，對局中斷線保留座位 20 秒（未在 E2E 中測試）。
- **身分**：訪客名稱直接放在 join options，沒有驗證。正式版要用 `onAuth` 驗證 token。

## 遇到的問題

1. 安裝 `colyseus` 0.18 總套件（含 playground、monitor、redis 等）時 npm 10.8.2 出現內部錯誤 `Cannot read properties of null (reading 'edgesOut')`，改成只裝 `@colyseus/core` 與 `@colyseus/ws-transport`。Vite 8 也有同樣問題，改用 Vite 7.3。
2. 為避免「伺服器在 `onJoin` 送出的第一個訊息比客戶端註冊 handler 早到」（在 Nakama 分支實際遇過），客戶端註冊好 handler 後送 `ready`，伺服器才回傳完整狀態。
3. 0.18 的文件與網路範例多半還是 0.15／0.16 的寫法（例如套件名稱 `colyseus.js` 已改為 `@colyseus/sdk`），需要直接看型別定義確認 API。

## 粗估實作時間

以 AI 協助開發計（前端畫面、E2E 腳本沿用 Nakama 分支）：伺服器約 30 分鐘、前端 adapter 約 10 分鐘、E2E／資料庫驗證調整約 10 分鐘。人工從零開發預估 1～1.5 天，比 Nakama 少的部分主要是不用處理 tick 與 ES5 限制，多的部分是資料庫、玩家列表、聊天要自己寫。
