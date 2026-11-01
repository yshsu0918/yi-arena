# 歷史文件

這個資料夾放已不採用或已完成的研究、草案與實驗紀錄，保留作為決策依據。目前的文件見 [roadmap.md](../roadmap.md)、[feature-list.md](../feature-list.md)、[spec_v1.md](../spec_v1.md)、[decisions.md](../decisions.md)。

| 文件 | 內容 | 狀態 |
| --- | --- | --- |
| [platform-research.md](platform-research.md) | 早期研究：大廳與遊戲邏輯分層、候選框架比較、五子棋引擎 | 已完成 |
| [poc-test-plan.md](poc-test-plan.md) | Nakama、Colyseus、boardgame.io 三框架 PoC 的測試計畫 | 已完成 |
| [poc-results.md](poc-results.md) | 三框架 PoC 的比較結果 | 已完成 |
| [architecture-colyseus.md](architecture-colyseus.md) | 大廳與對局都放在 Colyseus 的架構草案，附錄為與規格 v1 的差異 | 已不採用 |
| [design-discussion.md](design-discussion.md) | 設計討論的原始紀錄：通用房間、邀局、NNGS 參考、聊天隔離、圍棋規則庫、悔棋、計時 | 已整理進規格與決策紀錄 |
| [plan-doc-restructure.md](plan-doc-restructure.md) | 文件結構調整規劃 | 已執行 |
| [nngs-test/](nngs-test/README.md) | NNGS 1.1.22 的編譯步驟、測試腳本與紀錄 | 已完成，結論見 [設計討論紀錄](design-discussion.md) 5.6、5.7 節 |

各框架 PoC 的程式碼在 `poc/nakama`、`poc/colyseus`、`poc/boardgameio` 分支；Go 大廳 PoC 在 `poc/go-lobby` 分支。
