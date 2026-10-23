[简体中文](README.md) | **繁體中文** | [English](README.en.md)

# Y-HTTP-Bench · HTTP 壓測工具

[![Release](https://img.shields.io/github/v/release/kite88/y-http-bench)](https://github.com/kite88/y-http-bench/releases/latest)
[![License](https://img.shields.io/github/license/kite88/y-http-bench)](LICENSE)

用 Go（純標準庫）寫的 HTTP 壓測（benchmark）工具，單檔案、零依賴、開箱即用。

## 編譯

需要 Go 1.21 及以上。

```bash
# 只編當前平臺
go build -o y-http-bench .
# Windows
go build -o y-http-bench.exe .
```

### 交叉編譯全部平臺

```powershell
.\build.ps1                      # Windows（PowerShell 5.1 / 7）
.\build.ps1 -OutDir D:\tmp\dist  # 換輸出目錄
```

```bash
./build.sh                       # macOS / Linux
./build.sh -o /tmp/yhb-dist      # 換輸出目錄
```

兩個指令碼跑同一份目標矩陣、呼叫同一個打包工具，**產物逐位元組一致**（校驗和相同），預設都輸出到
`dist/`（已被 `.gitignore` 忽略）。

專案只用標準庫，指令碼統一以 `CGO_ENABLED=0` 構建靜態可執行檔案。產物按業界慣例分兩種格式：
**Windows 出 zip，其餘平臺出 `.tar.gz`**（zip 不儲存 Unix 執行位，tar.gz 才能讓 Linux / macOS
使用者解壓後直接執行）。歸檔裡都只放一個短名的可執行檔案：

```text
dist/
├── y-http-bench-windows-amd64.zip     →  y-http-bench.exe
├── y-http-bench-linux-arm64.tar.gz    →  y-http-bench   （帶 0755 可執行位）
├── y-http-bench-darwin-arm64.tar.gz   →  y-http-bench
├── ……                                 （共 15 個平臺）
├── y-http-bench(.exe)                 ← 本機平臺的未壓縮版（如 linux/amd64、windows/amd64），本機自用、不釋出
└── checksums.txt                      ← 15 個歸檔的 SHA256（LF 換行，可 sha256sum -c）
```

本機平臺那一份不壓縮地留在 `dist/` 裡，日常直接跑，不用解壓：

```powershell
.\dist\y-http-bench.exe -url https://example.com -c 100 -n 10000
```

| 系統 | 架構 |
| --- | --- |
| Windows | amd64 / arm64 / 386 |
| Linux | amd64 / arm64 / 386 / arm(v7) / ppc64le / s390x / riscv64 / loong64 |
| macOS | amd64 / arm64 |
| FreeBSD | amd64 / arm64 |

矩陣就是 `build.ps1` 頂部的 `$targets`，增刪一行即可。指令碼相容 Windows PowerShell 5.1 與
PowerShell 7（`pwsh`），所以 Linux / macOS 裝了 pwsh 也能跑同一份指令碼。

> 交叉編譯時 `CGO_ENABLED=0`，域名解析走 Go 自帶的純 Go 解析器（不讀 `nsswitch.conf`，
> 個別內網域名解析行為可能與本機構建不同）；本機 `go build` 時保持系統預設行為。
>
> 歸檔由 `tools/pack` 生成，而不是呼叫系統 `tar` / `zip`：Windows 自帶的 `tar.exe`（精簡版
> bsdtar）不讀 Unix 許可權位、也不支援 `--mode`，它打出來的包在 Linux 上解壓是 644、跑不起來；
> `zip(1)` 在 macOS / Linux 上也不是必然存在，而且各實現寫出的位元組不一致。`pack` 用 Go 標準庫
> 顯式寫入 0755，並把時間戳固定下來（tar.gz 顯示 1970-01-01，zip 顯示 zip 的紀元 1980-01-01），
> 所以同原始碼無論在哪個平臺、用哪個指令碼構建，產物和校驗和都可復現。

### 發版

推 `v*` 標籤即可，workflow（`.github/workflows/release.yml`）會自動交叉編譯全部平臺並建立
GitHub Release：

```bash
git tag v1.0.1 && git push origin v1.0.1
```

流程先跑 `go vet ./...`，再用 `sha256sum -c checksums.txt` 自檢產物，任一步失敗都不會發版；
標籤名含 `-`（如 `v1.1.0-rc1`）會自動標記為 prerelease，不會頂掉 Latest。

需要手工發版、或想在本地完整試跑一遍流程時，把 `dist/` 裡的 `*.zip`、`*.tar.gz` 和
`checksums.txt` 傳到 Releases 即可：本機版那個未壓縮的二進位制不用上傳，Windows PowerShell 不展開
萬用字元、需要逐個列出檔名。

## 快速開始

```bash
# 固定請求數：20000 次請求，100 併發
y-http-bench -url https://example.com -c 100 -n 20000

# 固定時長：50 併發壓 30 秒
y-http-bench -url https://example.com -c 50 -d 30s

# 限速：穩定 1000 QPS 打 1 分鐘
y-http-bench -url https://api.test/users -c 50 -d 1m -qps 1000

# POST 介面：自定義請求頭 + 請求體
y-http-bench -url https://api.test/login -m POST \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer xxx" \
  -body '{"user":"tom","pwd":"123"}' -c 20 -n 5000

# 從檔案讀請求體 + 匯出原始延遲資料用於畫圖
y-http-bench -url https://api.test/order -m POST -H "Content-Type: application/json" \
  -body-file ./payload.json -c 30 -d 20s -dump-latency latency.csv
```

`-n` 和 `-d` 至少要指定一個（否則測試不會結束）。兩者同時給定時，先到達的條件觸發結束。
執行中按 `Ctrl+C` 會停止壓測，並基於已採集的樣本照常輸出報告。

## 引數

| 引數 | 預設值 | 說明 |
| --- | --- | --- |
| `-url` | 無（必填） | 目標 URL |
| `-m` | `GET` | HTTP 方法 |
| `-c` | `10` | 併發數 |
| `-n` | `0` | 總請求數，`0` 表示不限（配合 `-d`） |
| `-d` | `0` | 壓測時長，如 `10s` / `2m`，`0` 表示不限（配合 `-n`） |
| `-qps` | `0` | 全侷限速（請求/秒），`0` 表示不限速 |
| `-timeout` | `10s` | 單次請求超時 |
| `-H` | 無 | 自定義請求頭，可重複傳入 |
| `-body` | 空 | 請求體字串 |
| `-body-file` | 空 | 從檔案讀取請求體 |
| `-keepalive` | `true` | 是否複用連線，置為 `false` 可模擬短連線（每次握手） |
| `-insecure` | `false` | 跳過 TLS 證書校驗（自簽證書場景） |
| `-dump-latency` | 空 | 把每次請求延遲流式匯出為 CSV（列：latency_ms,status,bytes,error），不佔用額外記憶體 |
| `-v` | `false` | 列印全部錯誤分類（預設最多顯示 5 類） |

## 輸出說明

壓測過程中每 500ms 重新整理一行實時進度（耗時 / 已完成 / 實時 QPS / 失敗數）；結束後輸出報告：

- 概覽：總請求數、失敗數、成功率、平均 QPS、吞吐量（設了 `-qps` 時額外顯示目標 QPS 與速率達成率）
- 延遲分佈：Min / Avg / P50 / P90 / P95 / P99 / P99.9 / Max（毫秒，讀取完響應體為止；取直方圖桶中點近似，
  相對誤差上限約 0.1%）
- 狀態碼分佈
- 錯誤分佈（網路層錯誤按型別聚合）

延遲口徑：只統計拿到響應的請求（含 4xx/5xx），網路層錯誤只計入失敗數與錯誤分佈；
設了 `-qps` 時，計時起點是請求的「理想發出時刻」，排隊等待時間也算進延遲。

## 實現要點

- **限速器不用 `time.Ticker`**：Windows 上 ticker 最小間隔受系統定時器精度限制（約 15.6ms），
  會把吞吐死死卡在 60~90 req/s（實測設定 5000 只跑出 88）。這裡改用「絕對時間表」排程：
  第 n 個請求的理想發出時刻 = `start + n×interval`，只依賴 `time.Now()` 計算等待時間，
  因此不受定時器解析度約束。實測 200 / 1000 / 5000 三檔設定分別跑出 188 / 985 / 4982 req/s。
- **限速模式下延遲從「理想發出時刻」起算**（coordinated omission 修正）：`Limiter.Wait()` 返回本請求
  本應發出的時刻，計時起點取它而不是 `client.Do` 之前的一瞬。這樣被限速器推遲、或上一批請求把 worker
  拖慢所積累的排隊時間都會計入延遲——否則高負載下 P99 會明顯偏樂觀（wrk2 的 fixed-rate 模式同理）。
  報告裡的「速率達成率」用來判斷服務端是否跟得上目標速率。
- **延遲統計用直方圖，不保留全量樣本**：`Histogram` 是 HdrHistogram 的簡化實現——對數線性分桶，
  1024 桶/量級共 32 個量級（固定 256KB，與請求量無關），可表示到約 36 分鐘，相對誤差上限約 0.1%；
  分位數取桶中點並夾到 `[Min, Max]` 之間，避免出現「P99.9 大於 Max」的觀感問題。
- **`-dump-latency` 流式落盤**：合併樣本時順手寫進 `bufio.Writer`，不再把全量樣本留在記憶體裡等結束再寫。
- **每個 worker 本地攢樣本**，滿 4096 條才合併一次，避免高頻搶同一把鎖。
- **顯式開啟 HTTP/2**：手寫 `http.Transport` 時 Go 不會自動啟用 HTTP/2，需要 `ForceAttemptHTTP2: true`；
  HTTPS 目標會協商到 h2（明文 h2c 不在 `net/http` 支援範圍內）。
- **表頭按顯示寬度對齊**：中文在終端佔 2 個顯示列，而 `fmt` 的 `%-8s` 按字元個數補空格，
  直接用會讓中文表頭比資料行更寬、整體錯位。因此用 `padRight` / `displayWidth`（含全形與 emoji 寬度判定）自己補空格。
- **請求體 Body 每次新建 reader**：`*strings.Reader` 不能被多個 goroutine 同時讀。
- **結束時的在途請求不計入失敗**：`Ctrl+C` 或時長到期時被 cancel 的請求不算服務端錯誤。

## 已知侷限

1. 仍是**閉環（closed-loop）壓測**：每個 worker 必須等上一次響應讀完才發下一次。設了 `-qps` 時排隊時間
   會被補進延遲，但真要「按固定速率發出、不被服務端拖慢」，需要開環模型（按時間表預生成請求 + 獨立
   的結果收集佇列），這會讓記憶體與複雜度都上一個臺階。
2. 單機單程序，沒有多 URL / 多階段場景、爬坡（ramp-up）、think time、斷言與 SLA 閾值判定，也不支援分散式。
3. 每請求一次分配（`Request.Clone` + 新建 Body reader）寫起來乾淨，但會帶來 GC 壓力；極限吞吐下不如
   wrk 這類 epoll + 複用緩衝的實現。
4. 客戶端側指標沒有拆分統計：連線複用次數、DNS / TCP / TLS 各階段耗時都沒有單獨列出，
   壓 HTTPS 站點時想定位握手段的瓶頸需要另配工具。

## 壓測注意事項

1. 壓的是被測服務，不是本機網路：儘量在與被測機同機房/內網的中立機器上跑。
2. 結果要先區分瓶頸在哪一方——觀察被測機 CPU、GC、連線池是否打滿。
3. 關注 P99 / P99.9 而不是平均值，平均值容易被大量快請求掩蓋長尾。
4. Windows 下臨時埠回收較慢，短連線高壓場景可能出現 `connectex` 拒絕，屬被測端或系統限制。

## 許可證

[MIT](LICENSE)
