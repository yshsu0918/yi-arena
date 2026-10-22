# Nakama PoC 測試結果

- 測試日期：2026-10-22
- 版本：Nakama 3.41.0、Postgres 16、nakama-js 2.8.0、Vue 3.5、Vite 7.3、Playwright 1.64（Chromium headless）
- 執行方式：`./scripts/test.sh`（建置 → `docker compose down -v` 清空資料庫 → 啟動 → E2E → 資料庫驗證）
- 本目錄的截圖、`e2e-result.json`、`db-check.txt` 來自 `TICK_RATE=1` 的一次完整執行。

## 結果摘要

E2E 19 項檢查、資料庫 14 項檢查全部通過。

| 檢查項目 | 結果 | 說明 |
| --- | --- | --- |
| 大廳列表 | ✅ | 單一「主大廳」，顯示線上人數 |
| 房間列表（即時更新） | ✅（輪詢） | `listMatches` 每秒輪詢；Nakama 沒有推送房間列表變化的內建機制 |
| 玩家列表（有／無） | ✅ 有 | 用內建聊天頻道 `lobby` 的 presence；玩家狀態由房間 label 推算 |
| 兩種遊戲都能建房與對戰 | ✅ | 井字棋、五子棋（freestyle）共用同一個 match handler |
| 兩場同時進行互不干擾 | ✅ | 兩房間以 `Promise.all` 同時落子，棋盤與紀錄皆獨立 |
| 已滿房間拒絕加入 | ✅ | `matchJoinAttempt` 回傳 `accept: false`，畫面顯示「房間已滿」 |
| 房內聊天與隔離 | ✅ 內建 | 內建 room chat channel（以 match id 命名），訊息自動存入 `message` 表 |
| 對局紀錄寫入資料庫 | ✅ | 對局結束時用 `nk.storageWrite` 寫入 `storage` 表（collection `game_records`） |
| 返回大廳 | ✅ | 雙方離開後對局在下一個 tick 結束並從列表消失；之前顯示「已結束」 |
| 非法落子被拒絕 | ✅ | 不是自己回合、落在已有棋子的格子都由伺服器拒絕並回傳錯誤訊息 |

N/A 項目：無。

## 落子延遲（tick 頻率）

延遲是從點擊到自己畫面出現棋子的時間，E2E 每 100 ms 檢查一次畫面，所以數字含最多約 100 ms 的量測誤差。

| TICK_RATE | 平均 | 最小 | 最大 | 樣本 |
| --- | --- | --- | --- | --- |
| 1（官方建議回合制的最低值） | 874 ms | 112 ms | 1048 ms | 14 |
| 10 | 117 ms | 112 ms | 134 ms | 14 |

結論：tick 1 的延遲平均接近 1 秒，人工下棋可以接受但手感明顯遲滯；tick 5～10 的延遲幾乎感覺不到，伺服器負擔對回合制而言仍然很低。正式版建議 5 以上。用 `TICK_RATE=10 ./scripts/test.sh` 可重跑。

## 實作方式與自行實作的部分

- **擴充語言選 TypeScript**（編譯成 ES5 單一檔案給 goja 執行）。原因：前端也是 TypeScript；Go 外掛必須用與 Nakama 二進位檔完全相同的 Go 版本與相依套件編譯（需另外用 pluginbuilder 映像），PoC 階段成本較高。代價是 goja 只支援 ES5、不能用 npm 套件；規則模組因此寫成不依賴任何東西的 ES5 相容程式碼。
- **規則模組** `server/src/rules.ts`：純函式（`newGame`、`currentTurn`、`validateMove`、`applyMove`、`replay`），6 個單元測試。
- **房間 = authoritative match**：`create_room` RPC 呼叫 `nk.matchCreate`；房間資訊放在 match label（JSON）。
- **座位在 `matchJoinAttempt` 就保留**，避免同一個 tick 內兩人同時取得最後一個座位。座位綁 userId，對局中斷線可回原座位（重連未在 E2E 中測試）。
- **聊天隔離**：Nakama 的 room 頻道任何人只要知道名稱就能加入，所以加了 `registerRtBefore('ChannelJoin')` 檢查加入者是否是該房間的玩家。這個 hook 已實作，但 E2E 只驗證了畫面層面的隔離，沒有自動測試「P5 直接呼叫 joinChat 被拒絕」。
- **對外埠 8901**：nginx 在 8901 提供 Vue 靜態檔並反向代理 `/v2`（HTTP API）與 `/ws`（即時連線）到 Nakama 的 7350。管理後台在 7351（帳密 admin / password）。

## 遇到的問題

1. **前端漏收第一個狀態**：`matchJoin` 中廣播的狀態有時比 `joinMatch()` 的 promise 先到，客戶端因為還沒記下 match id 而把它丟掉。tick 1 時剛好沒發生，tick 10 時穩定重現。修正：在呼叫 `joinMatch` 之前先記下 match id。這是 Nakama 客戶端常見的陷阱，值得記在開發規範裡。
2. Vite 8 在 npm 10.8.2 下安裝失敗（npm 內部錯誤 `Cannot read properties of null (reading 'edgesOut')`），改用 Vite 7.3。與 Nakama 無關。

## 粗估實作時間

以 AI 協助開發計：規則模組與測試約 15 分鐘、Nakama 伺服器模組約 30 分鐘、前端（三個分支共用的畫面）約 30 分鐘、E2E 與資料庫驗證約 30 分鐘、除錯約 10 分鐘。人工開發預估 1.5～2 天。
