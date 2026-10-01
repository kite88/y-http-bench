# y-http-bench · HTTP 压测工具

简体中文 | [English](README.en.md)

[![Release](https://img.shields.io/github/v/release/kite88/y-http-bench)](https://github.com/kite88/y-http-bench/releases/latest)
[![License](https://img.shields.io/github/license/kite88/y-http-bench)](LICENSE)

用 Go（纯标准库）写的 HTTP 压测（benchmark）工具，单文件、零依赖、开箱即用。

## 编译

需要 Go 1.21 及以上。

```bash
# 只编当前平台
go build -o y-http-bench .
# Windows
go build -o y-http-bench.exe .
```

### 交叉编译全部平台

```powershell
.\build.ps1                      # Windows（PowerShell 5.1 / 7）
.\build.ps1 -OutDir D:\tmp\dist  # 换输出目录
```

```bash
./build.sh                       # macOS / Linux
./build.sh -o /tmp/yhb-dist      # 换输出目录
```

两个脚本跑同一份目标矩阵、调用同一个打包工具，**产物逐字节一致**（校验和相同），默认都输出到
`dist/`（已被 `.gitignore` 忽略）。

项目只用标准库，脚本统一以 `CGO_ENABLED=0` 构建静态可执行文件。产物按业界惯例分两种格式：
**Windows 出 zip，其余平台出 `.tar.gz`**（zip 不保存 Unix 执行位，tar.gz 才能让 Linux / macOS
用户解压后直接运行）。归档里都只放一个短名的可执行文件：

```text
dist/
├── y-http-bench-windows-amd64.zip     →  y-http-bench.exe
├── y-http-bench-linux-arm64.tar.gz    →  y-http-bench   （带 0755 可执行位）
├── y-http-bench-darwin-arm64.tar.gz   →  y-http-bench
├── ……                                 （共 15 个平台）
├── y-http-bench(.exe)                 ← 本机平台的未压缩版（如 linux/amd64、windows/amd64），本机自用、不发布
└── checksums.txt                      ← 15 个归档的 SHA256（LF 换行，可 sha256sum -c）
```

本机平台那一份不压缩地留在 `dist/` 里，日常直接跑，不用解压：

```powershell
.\dist\y-http-bench.exe -url https://example.com -c 100 -n 10000
```

| 系统 | 架构 |
| --- | --- |
| Windows | amd64 / arm64 / 386 |
| Linux | amd64 / arm64 / 386 / arm(v7) / ppc64le / s390x / riscv64 / loong64 |
| macOS | amd64 / arm64 |
| FreeBSD | amd64 / arm64 |

矩阵就是 `build.ps1` 顶部的 `$targets`，增删一行即可。脚本兼容 Windows PowerShell 5.1 与
PowerShell 7（`pwsh`），所以 Linux / macOS 装了 pwsh 也能跑同一份脚本。

> 交叉编译时 `CGO_ENABLED=0`，域名解析走 Go 自带的纯 Go 解析器（不读 `nsswitch.conf`，
> 个别内网域名解析行为可能与本机构建不同）；本机 `go build` 时保持系统默认行为。
>
> 归档由 `tools/pack` 生成，而不是调用系统 `tar` / `zip`：Windows 自带的 `tar.exe`（精简版
> bsdtar）不读 Unix 权限位、也不支持 `--mode`，它打出来的包在 Linux 上解压是 644、跑不起来；
> `zip(1)` 在 macOS / Linux 上也不是必然存在，而且各实现写出的字节不一致。`pack` 用 Go 标准库
> 显式写入 0755，并把时间戳固定下来（tar.gz 显示 1970-01-01，zip 显示 zip 的纪元 1980-01-01），
> 所以同源码无论在哪个平台、用哪个脚本构建，产物和校验和都可复现。

### 发版

推 `v*` 标签即可，workflow（`.github/workflows/release.yml`）会自动交叉编译全部平台并创建
GitHub Release：

```bash
git tag v1.0.1 && git push origin v1.0.1
```

流程先跑 `go vet ./...`，再用 `sha256sum -c checksums.txt` 自检产物，任一步失败都不会发版；
标签名含 `-`（如 `v1.1.0-rc1`）会自动标记为 prerelease，不会顶掉 Latest。

需要手工发版、或想在本地完整试跑一遍流程时，把 `dist/` 里的 `*.zip`、`*.tar.gz` 和
`checksums.txt` 传到 Releases 即可：本机版那个未压缩的二进制不用上传，Windows PowerShell 不展开
通配符、需要逐个列出文件名。

## 快速开始

```bash
# 固定请求数：20000 次请求，100 并发
y-http-bench -url https://example.com -c 100 -n 20000

# 固定时长：50 并发压 30 秒
y-http-bench -url https://example.com -c 50 -d 30s

# 限速：稳定 1000 QPS 打 1 分钟
y-http-bench -url https://api.test/users -c 50 -d 1m -qps 1000

# POST 接口：自定义请求头 + 请求体
y-http-bench -url https://api.test/login -m POST \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer xxx" \
  -body '{"user":"tom","pwd":"123"}' -c 20 -n 5000

# 从文件读请求体 + 导出原始延迟数据用于画图
y-http-bench -url https://api.test/order -m POST -H "Content-Type: application/json" \
  -body-file ./payload.json -c 30 -d 20s -dump-latency latency.csv
```

`-n` 和 `-d` 至少要指定一个（否则测试不会结束）。两者同时给定时，先到达的条件触发结束。
运行中按 `Ctrl+C` 会停止压测，并基于已采集的样本照常输出报告。

## 参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-url` | 无（必填） | 目标 URL |
| `-m` | `GET` | HTTP 方法 |
| `-c` | `10` | 并发数 |
| `-n` | `0` | 总请求数，`0` 表示不限（配合 `-d`） |
| `-d` | `0` | 压测时长，如 `10s` / `2m`，`0` 表示不限（配合 `-n`） |
| `-qps` | `0` | 全局限速（请求/秒），`0` 表示不限速 |
| `-timeout` | `10s` | 单次请求超时 |
| `-H` | 无 | 自定义请求头，可重复传入 |
| `-body` | 空 | 请求体字符串 |
| `-body-file` | 空 | 从文件读取请求体 |
| `-keepalive` | `true` | 是否复用连接，置为 `false` 可模拟短连接（每次握手） |
| `-insecure` | `false` | 跳过 TLS 证书校验（自签证书场景） |
| `-dump-latency` | 空 | 把每次请求延迟流式导出为 CSV（列：latency_ms,status,bytes,error），不占用额外内存 |
| `-v` | `false` | 打印全部错误分类（默认最多显示 5 类） |

## 输出说明

压测过程中每 500ms 刷新一行实时进度（耗时 / 已完成 / 实时 QPS / 失败数）；结束后输出报告：

- 概览：总请求数、失败数、成功率、平均 QPS、吞吐量（设了 `-qps` 时额外显示目标 QPS 与速率达成率）
- 延迟分布：Min / Avg / P50 / P90 / P95 / P99 / P99.9 / Max（毫秒，读取完响应体为止；取直方图桶中点近似，
  相对误差上限约 0.1%）
- 状态码分布
- 错误分布（网络层错误按类型聚合）

延迟口径：只统计拿到响应的请求（含 4xx/5xx），网络层错误只计入失败数与错误分布；
设了 `-qps` 时，计时起点是请求的「理想发出时刻」，排队等待时间也算进延迟。

## 实现要点

- **限速器不用 `time.Ticker`**：Windows 上 ticker 最小间隔受系统定时器精度限制（约 15.6ms），
  会把吞吐死死卡在 60~90 req/s（实测设定 5000 只跑出 88）。这里改用「绝对时间表」调度：
  第 n 个请求的理想发出时刻 = `start + n×interval`，只依赖 `time.Now()` 计算等待时间，
  因此不受定时器分辨率约束。实测 200 / 1000 / 5000 三档设定分别跑出 188 / 985 / 4982 req/s。
- **限速模式下延迟从「理想发出时刻」起算**（coordinated omission 修正）：`Limiter.Wait()` 返回本请求
  本应发出的时刻，计时起点取它而不是 `client.Do` 之前的一瞬。这样被限速器推迟、或上一批请求把 worker
  拖慢所积累的排队时间都会计入延迟——否则高负载下 P99 会明显偏乐观（wrk2 的 fixed-rate 模式同理）。
  报告里的「速率达成率」用来判断服务端是否跟得上目标速率。
- **延迟统计用直方图，不保留全量样本**：`Histogram` 是 HdrHistogram 的简化实现——对数线性分桶，
  1024 桶/量级共 32 个量级（固定 256KB，与请求量无关），可表示到约 36 分钟，相对误差上限约 0.1%；
  分位数取桶中点并夹到 `[Min, Max]` 之间，避免出现「P99.9 大于 Max」的观感问题。
- **`-dump-latency` 流式落盘**：合并样本时顺手写进 `bufio.Writer`，不再把全量样本留在内存里等结束再写。
- **每个 worker 本地攒样本**，满 4096 条才合并一次，避免高频抢同一把锁。
- **显式开启 HTTP/2**：手写 `http.Transport` 时 Go 不会自动启用 HTTP/2，需要 `ForceAttemptHTTP2: true`；
  HTTPS 目标会协商到 h2（明文 h2c 不在 `net/http` 支持范围内）。
- **表头按显示宽度对齐**：中文在终端占 2 个显示列，而 `fmt` 的 `%-8s` 按字符个数补空格，
  直接用会让中文表头比数据行更宽、整体错位。因此用 `padRight` / `displayWidth`（含全角与 emoji 宽度判定）自己补空格。
- **请求体 Body 每次新建 reader**：`*strings.Reader` 不能被多个 goroutine 同时读。
- **结束时的在途请求不计入失败**：`Ctrl+C` 或时长到期时被 cancel 的请求不算服务端错误。

## 已知局限

1. 仍是**闭环（closed-loop）压测**：每个 worker 必须等上一次响应读完才发下一次。设了 `-qps` 时排队时间
   会被补进延迟，但真要「按固定速率发出、不被服务端拖慢」，需要开环模型（按时间表预生成请求 + 独立
   的结果收集队列），这会让内存与复杂度都上一个台阶。
2. 单机单进程，没有多 URL / 多阶段场景、爬坡（ramp-up）、think time、断言与 SLA 阈值判定，也不支持分布式。
3. 每请求一次分配（`Request.Clone` + 新建 Body reader）写起来干净，但会带来 GC 压力；极限吞吐下不如
   wrk 这类 epoll + 复用缓冲的实现。
4. 客户端侧指标没有拆分统计：连接复用次数、DNS / TCP / TLS 各阶段耗时都没有单独列出，
   压 HTTPS 站点时想定位握手段的瓶颈需要另配工具。

## 压测注意事项

1. 压的是被测服务，不是本机网络：尽量在与被测机同机房/内网的中立机器上跑。
2. 结果要先区分瓶颈在哪一方——观察被测机 CPU、GC、连接池是否打满。
3. 关注 P99 / P99.9 而不是平均值，平均值容易被大量快请求掩盖长尾。
4. Windows 下临时端口回收较慢，短连接高压场景可能出现 `connectex` 拒绝，属被测端或系统限制。

## 许可证

[MIT](LICENSE)
