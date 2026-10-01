[简体中文](README.md) | English

# y-http-bench · HTTP Benchmark Tool

[![Release](https://img.shields.io/github/v/release/kite88/y-http-bench)](https://github.com/kite88/y-http-bench/releases/latest)
[![License](https://img.shields.io/github/license/kite88/y-http-bench)](LICENSE)

A HTTP load testing (benchmark) tool written in Go (pure standard library) — single file, zero dependencies, works out of the box.

## Build

Requires Go 1.21 or later.

```bash
# Build for the current platform only
go build -o y-http-bench .
# Windows
go build -o y-http-bench.exe .
```

### Cross-compile for all platforms

```powershell
.\build.ps1                      # Windows (PowerShell 5.1 / 7)
.\build.ps1 -OutDir D:\tmp\dist  # Custom output directory
```

```bash
./build.sh                       # macOS / Linux
./build.sh -o /tmp/yhb-dist      # Custom output directory
```

Both scripts consume the same target matrix and invoke the same packaging tool, so the
artifacts are **byte-for-byte identical** (matching checksums). Both output to `dist/`
by default (already ignored via `.gitignore`).

The project only uses the standard library, and both scripts build statically linked
executables with `CGO_ENABLED=0`. Artifacts come in two formats, following common
conventions: **Windows gets zip, every other platform gets `.tar.gz`** (zip does not
preserve the Unix executable bit; tar.gz lets Linux / macOS users run the binary right
after extraction). Each archive contains a single, short-named executable:

```text
dist/
├── y-http-bench-windows-amd64.zip     →  y-http-bench.exe
├── y-http-bench-linux-arm64.tar.gz    →  y-http-bench   (with 0755 executable bit)
├── y-http-bench-darwin-arm64.tar.gz   →  y-http-bench
├── ……                                 (15 platforms in total)
├── y-http-bench(.exe)                 ← uncompressed build for the local platform
│                                         (e.g. linux/amd64, windows/amd64); for local
│                                         use only, not published
└── checksums.txt                      ← SHA256 of the 15 archives (LF line endings,
                                         verifiable with sha256sum -c)
```

The local platform's build stays uncompressed in `dist/`, so you can run it directly
without extracting:

```powershell
.\dist\y-http-bench.exe -url https://example.com -c 100 -n 10000
```

| OS | Architectures |
| --- | --- |
| Windows | amd64 / arm64 / 386 |
| Linux | amd64 / arm64 / 386 / arm(v7) / ppc64le / s390x / riscv64 / loong64 |
| macOS | amd64 / arm64 |
| FreeBSD | amd64 / arm64 |

The matrix is simply the `$targets` array at the top of `build.ps1` — add or remove a
line to change it. The scripts are compatible with both Windows PowerShell 5.1 and
PowerShell 7 (`pwsh`), so Linux / macOS users with pwsh installed can run the very same
script.

> During cross-compilation `CGO_ENABLED=0` is set, so DNS resolution uses Go's built-in
> pure Go resolver (it does not read `nsswitch.conf`; resolution of some intranet
> domains may behave differently than a local build). A local `go build` keeps the
> system default behavior.
>
> Archives are produced by `tools/pack` rather than the system `tar` / `zip`: the
> `tar.exe` bundled with Windows (a stripped-down bsdtar) ignores Unix permission bits
> and does not support `--mode`, so archives it produces extract as 644 on Linux and
> won't run; `zip(1)` is not necessarily present on macOS / Linux, and different
> implementations write different bytes. `pack` uses the Go standard library to
> explicitly write 0755 and pins timestamps (tar.gz shows 1970-01-01, zip shows the zip
> epoch 1980-01-01), so builds from the same source are reproducible on every platform
> with either script — artifacts and checksums included.

### Release

Just push a `v*` tag; the workflow (`.github/workflows/release.yml`) automatically
cross-compiles for all platforms and creates a GitHub Release:

```bash
git tag v1.0.1 && git push origin v1.0.1
```

The pipeline first runs `go vet ./...`, then verifies artifacts with
`sha256sum -c checksums.txt`; a release is cut only if every step passes. Tag names
containing `-` (e.g. `v1.1.0-rc1`) are automatically marked as a prerelease and won't
take over Latest.

For a manual release, or to dry-run the full pipeline locally, upload the `*.zip`,
`*.tar.gz` and `checksums.txt` from `dist/` to Releases. The uncompressed local-platform
binary does not need to be uploaded; note that Windows PowerShell does not expand
wildcards, so file names must be listed one by one.

## Quick Start

```bash
# Fixed request count: 20000 requests at 100 concurrency
y-http-bench -url https://example.com -c 100 -n 20000

# Fixed duration: 50 concurrent workers for 30 seconds
y-http-bench -url https://example.com -c 50 -d 30s

# Rate limited: steady 1000 QPS for 1 minute
y-http-bench -url https://api.test/users -c 50 -d 1m -qps 1000

# POST endpoint: custom headers + body
y-http-bench -url https://api.test/login -m POST \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer xxx" \
  -body '{"user":"tom","pwd":"123"}' -c 20 -n 5000

# Read request body from file + export raw latency data for plotting
y-http-bench -url https://api.test/order -m POST -H "Content-Type: application/json" \
  -body-file ./payload.json -c 30 -d 20s -dump-latency latency.csv
```

At least one of `-n` and `-d` must be specified (otherwise the test never ends). If
both are given, whichever condition is reached first ends the test. Pressing `Ctrl+C`
during a run stops the benchmark and still prints a report based on the samples
collected so far.

## Flags

| Flag | Default | Description |
| --- | --- | --- |
| `-url` | none (required) | Target URL |
| `-m` | `GET` | HTTP method |
| `-c` | `10` | Concurrency (number of workers) |
| `-n` | `0` | Total number of requests; `0` means unlimited (use with `-d`) |
| `-d` | `0` | Test duration, e.g. `10s` / `2m`; `0` means unlimited (use with `-n`) |
| `-qps` | `0` | Global rate limit (requests/second); `0` means unlimited |
| `-timeout` | `10s` | Per-request timeout |
| `-H` | none | Custom header; may be repeated |
| `-body` | empty | Request body as a string |
| `-body-file` | empty | Read the request body from a file |
| `-keepalive` | `true` | Reuse connections; set to `false` to simulate short-lived connections (new handshake per request) |
| `-insecure` | `false` | Skip TLS certificate verification (self-signed certificates) |
| `-dump-latency` | empty | Stream per-request latencies to a CSV file (columns: latency_ms,status,bytes,error); uses no extra memory |
| `-v` | `false` | Print all error categories (at most 5 are shown by default) |

## Output

During the run, a live progress line refreshes every 500ms (elapsed time / completed /
current QPS / failures). When the run finishes, a report is printed:

- Overview: total requests, failures, success rate, average QPS, throughput (with
  `-qps` set, the target QPS and achieved-rate ratio are also shown)
- Latency distribution: Min / Avg / P50 / P90 / P95 / P99 / P99.9 / Max (in
  milliseconds, measured until the response body has been fully read; approximated by
  the histogram bucket midpoint, with a relative error bound of roughly 0.1%)
- Status code distribution
- Error distribution (network-layer errors aggregated by type)

Latency accounting: only requests that received a response are counted (including
4xx/5xx); network-layer errors only contribute to the failure count and error
distribution. When `-qps` is set, the timer starts at the request's "ideal send time",
so time spent waiting in the queue is included in the latency.

## Implementation Notes

- **The rate limiter does not use `time.Ticker`**: on Windows the minimum ticker
  interval is limited by the system timer resolution (~15.6ms), which caps throughput
  at 60–90 req/s (in testing, a configured 5000 only achieved 88). Instead, scheduling
  uses an "absolute schedule": the ideal send time of the n-th request is
  `start + n×interval`, and the wait is computed from `time.Now()` only, so timer
  resolution doesn't matter. Measured results: configured rates of 200 / 1000 / 5000
  achieved 188 / 985 / 4982 req/s.
- **In rate-limited mode, latency is measured from the "ideal send time"** (a fix for
  coordinated omission): `Limiter.Wait()` returns the moment the request should have
  been sent, and the timer starts there rather than right before `client.Do`. This way,
  queueing time accumulated by rate limiting or by workers being slowed down by earlier
  requests is included in latency — otherwise P99 looks too optimistic under heavy load
  (same rationale as wrk2's fixed-rate mode). The "achieved-rate ratio" in the report
  tells you whether the server can keep up with the target rate.
- **Latency statistics use a histogram, not full samples**: `Histogram` is a
  simplified implementation of HdrHistogram — logarithmic-linear bucketing with 1024
  buckets per magnitude across 32 magnitudes (fixed 256KB, independent of request
  count), covering durations up to ~36 minutes with a relative error bound of ~0.1%.
  Quantiles take the bucket midpoint, clamped to `[Min, Max]` to avoid the confusing
  "P99.9 greater than Max" artifact.
- **`-dump-latency` streams to disk**: samples are written into a `bufio.Writer` as
  they are merged, instead of keeping all samples in memory until the end.
- **Each worker buffers samples locally**, merging only after 4096 entries, avoiding
  frequent contention on a single lock.
- **HTTP/2 is enabled explicitly**: when you hand-write an `http.Transport`, Go does
  not enable HTTP/2 automatically; `ForceAttemptHTTP2: true` is required. HTTPS targets
  negotiate h2 (cleartext h2c is outside `net/http`'s scope).
- **Table headers are aligned by display width**: CJK characters occupy 2 terminal
  columns, but `fmt`'s `%-8s` pads by character count, which would make CJK headers
  wider than data rows and misalign the whole table. Hence `padRight` / `displayWidth`
  (with full-width and emoji width detection) pad spaces manually.
- **The request body reader is recreated per request**: a `*strings.Reader` cannot be
  read by multiple goroutines concurrently.
- **In-flight requests at shutdown are not counted as failures**: requests cancelled by
  `Ctrl+C` or when the duration expires are not counted as server errors.

## Known Limitations

1. Still a **closed-loop** benchmark: each worker must wait for the previous response
   to finish before sending the next request. With `-qps` set, queueing time is added
   back into latency, but truly "sending at a fixed rate regardless of the server"
   requires an open-loop model (pre-generating requests on a schedule + an independent
   result collection queue), which would raise both memory usage and complexity a lot.
2. Single machine, single process; no multi-URL / multi-phase scenarios, ramp-up,
   think time, assertions or SLA threshold checks, and no distributed mode.
3. One allocation per request (`Request.Clone` + a fresh body reader) is clean to
   write, but adds GC pressure; at extreme throughput it can't match epoll-based
   implementations with buffer reuse such as wrk.
4. Client-side metrics are not broken down: connection reuse counts and DNS / TCP / TLS
   handshake timings are not reported separately; pinpointing handshake-phase
   bottlenecks when benchmarking HTTPS sites requires additional tooling.

## Benchmarking Tips

1. You are load-testing the target service, not your local network: run from a neutral
   machine in the same data center / intranet as the target whenever possible.
2. First figure out where the bottleneck is — watch the target machine's CPU, GC, and
   connection pool saturation.
3. Focus on P99 / P99.9 rather than averages; averages are easily skewed by masses of
   fast requests and hide the long tail.
4. On Windows, ephemeral ports are recycled slowly; under heavy short-connection load
   you may hit `connectex` rejections — this is a limitation of the target side or the
   OS, not the tool.

## License

[MIT](LICENSE)
