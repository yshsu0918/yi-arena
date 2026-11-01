# NNGS 實測紀錄

> 測試日期：2026-10-30。版本：NNGS 1.1.22（SourceForge 最新版，2009 年）。
> 環境：Ubuntu 24.04、gcc 13.3。所有玩家都是訪客帳號，對局皆不計分。

結論與設計上的取捨見 [設計討論紀錄](../design-discussion.md) 第 5.6、5.7 節，以及 [決策紀錄](../../decisions.md) D-16。

## 目錄

| 路徑 | 內容 |
| --- | --- |
| `mlrate-stub/` | 頂替 mlrate 的空殼（`rdbm.h`、`rdbm_stub.c`），讓 NNGS 可以在沒有等級分系統的情況下編譯 |
| `scripts/nngs_client.py` | 極簡 telnet 客戶端，供測試腳本使用 |
| `scripts/nngs_scenario1.py` | 情境 1：約局與還價、觀戰、講評、悔棋、點目、SGF |
| `scripts/nngs_scenario2.py` | 情境 2：沒人落子時自動判超時、斷線與續下 |
| `logs/scenario1.log`、`logs/scenario2.log` | 兩個情境的完整輸出（已移除 telnet 控制字元） |
| `logs/bob-alice-game1.sgf` | 情境 1 對局自動存下的 SGF |

## 編譯與啟動步驟

```bash
# 1. 下載並解壓縮
curl -L -o nngs.tar.gz https://sourceforge.net/projects/nngs/files/latest/download
tar -xzf nngs.tar.gz && cd nngs-1.1.22

# 2. mlrate 已找不到下載，改用空殼
mkdir -p mlrate/src
cp <repo>/docs/history/nngs-test/mlrate-stub/* mlrate/src/
gcc -c -o mlrate/src/rdbm_stub.o mlrate/src/rdbm_stub.c
ar rcs mlrate/src/libmlr.a mlrate/src/rdbm_stub.o

# 3. 編譯（新版 gcc 需要放寬參數）
./configure --prefix=$PWD/../install
make CFLAGS="-g -O2 -std=gnu89 -fcommon -w"
make install

# 4. 設定檔預期資料在 $prefix/nngssrv，實際裝在 $prefix/share/nngssrv
cd ../install
ln -sfn share/nngssrv nngssrv
for d in stats players games cgames problems spool intergo; do mkdir -p nngssrv/$d; done
for c in {a..z}; do mkdir -p nngssrv/players/$c nngssrv/games/$c nngssrv/cgames/$c; done
(cd nngssrv/messages && for f in *.default; do cp "$f" "${f%.default}"; done)
(cd nngssrv/ladder && for f in *.example; do cp "$f" "${f%.example}"; done)

# 5. 啟動（第一次啟動會在目前目錄產生 nngs.cnf），連線埠 9696
mkdir -p ../run && cd ../run && ../install/bin/nngssrv &

# 6. 跑測試腳本
python3 <repo>/docs/history/nngs-test/scripts/nngs_scenario1.py scenario1.log
python3 <repo>/docs/history/nngs-test/scripts/nngs_scenario2.py scenario2.log   # 約需 80 秒
```

編譯時遇到的問題：

1. **mlrate 找不到下載**：NNGS 編譯需要它的 `rdbm.h` 與 `libmlr.a`。改用空殼，`rdbm_open` 一律回傳 NULL，等級分功能停用。
2. **C++ 風格註解與 C89 衝突**：原本的 `-Wp,-std=c89` 會讓編譯失敗，改用 `-std=gnu89`。
3. **全域變數重複定義**：`channel.h` 在標頭檔裡定義 `carray`，gcc 10 以後預設 `-fno-common` 會連結失敗，要加 `-fcommon`。
4. **安裝路徑與設定檔不一致**：要手動建立連結與資料夾。
5. **關不掉**：送一般的結束訊號（SIGTERM）沒反應，只能用 `kill -9`。

## 測試結果

### 情境 1：alice（黑）vs bob（白），carol 觀戰

| 測試 | 結果 |
| --- | --- |
| 訪客登入 | 輸入沒註冊的名字即可以訪客身分登入，只能下不計分對局 |
| 約局 | alice 送 `match bob B 9 1 1`，bob 收到邀請與回覆方式 |
| 還價 | bob 回 `match alice W 9 2 1`（改成 2 分鐘），系統自動拒絕原提議並轉為 bob 提出新條件 |
| 接受 | alice 回發完全相同的條件即開局 |
| 大廳推送 | 開局時，其他線上玩家（carol）收到「Match 1: bob vs alice」 |
| 觀戰 | carol `observe 1` 後，每一手都即時推送給她 |
| 下錯順序 | alice 連下兩手被拒：「It is not your move.」 |
| 觀戰講評 | carol 的 `kibitz`，對局中的 alice 與 bob **都沒收到** |
| 棋手對話 | alice 的 `say` 只有 bob 收到，carol 看不到 |
| 悔棋 | alice 下 G7 後自己 `undo` 被拒（不是她的回合）；bob 輸入 `undo` 直接撤回，**不需要 alice 同意**；觀戰者收到「Undo in game 1」通知 |
| 點目 | 雙方 pass 後進入標記死子；雙方 `done` 後結算 |
| SGF | 自動存檔；被撤回的 G7 不在棋譜裡；講評與對話存成註解 |

### 情境 2：超時與斷線

| 測試 | 結果 |
| --- | --- |
| 自動判超時 | dave vs erin，1 分鐘包乾、沒人落子、沒人下 `time` 指令，約 1 分鐘後伺服器自動判黑方超時負，並推送給雙方 |
| 斷線 | frank vs gina 下了一手後 frank 斷線，對局**自動封盤**，gina 收到通知 |
| 續下 | gina 的 `stored` 顯示 0 盤；frank 重連後 `load gina-frank` 失敗。**訪客的對局斷線後不會保存，這盤就消失了**。註冊帳號的情況沒有測（註冊需管理員處理） |

### 發現的問題

1. **訪客斷線，對局消失**（見上）。
2. **點目結果怪異**：雙方各下兩手就 pass、`done`，結果為「W 6.5 B 0.0」，訊息卻是「alice has resigned the game」「Black resigns」。黑方的地沒有計入，結果類型也不對。
3. **剩餘時間顯示不一致**：2 分鐘的對局下了兩手後，`time` 顯示雙方都剩 1:00，與棋盤上顯示的時間不同。
4. **黑方時鐘在開局時就開始走**：carol 剛開始觀戰時（黑方還沒落子），黑方時間已從 02:00 變成 01:58。

### 沒有測的部分

- 註冊帳號、等級分、天梯（mlrate 已停用）。
- 封盤後續下（訪客無法保存對局）。
- 加拿大讀秒中的手數計算、`pause`／`addtime`。
- 讓子、改貼目、研究盤、連棋。
