# 歷史文件

這個資料夾放已不採用或已完成的研究、草案與實驗紀錄，保留作為決策依據。目前的路線與架構見 [roadmap.md](../roadmap.md) 與 [architecture_v1.md](../architecture_v1.md)。

| 文件 | 內容 | 狀態 |
| --- | --- | --- |
| [platform-research.md](platform-research.md) | 早期研究：大廳與遊戲邏輯分層、候選框架比較、五子棋引擎 | 已完成 |
| [poc-test-plan.md](poc-test-plan.md) | Nakama、Colyseus、boardgame.io 三框架 PoC 的測試計畫 | 已完成 |
| [poc-results.md](poc-results.md) | 三框架 PoC 的比較結果 | 已完成 |
| [architecture-colyseus.md](architecture-colyseus.md) | 大廳與對局都放在 Colyseus 的架構草案 | 已不採用 |
| [nngs-test/](nngs-test/README.md) | NNGS 1.1.22 的編譯步驟、測試腳本與紀錄 | 已完成，結論見 [設計討論紀錄](../design-discussion.md) 5.6、5.7 節 |

各框架 PoC 的程式碼在 `poc/nakama`、`poc/colyseus`、`poc/boardgameio` 分支；Go 大廳 PoC 在 `poc/go-lobby` 分支。
