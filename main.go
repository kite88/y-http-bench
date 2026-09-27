// y-http-bench 是一个纯 Go 标准库实现的 HTTP 压测工具。
//
// 用法示例：
//
//	y-http-bench -url https://example.com -c 100 -n 10000
//	y-http-bench -url https://example.com -c 50 -d 30s -qps 500
//	y-http-bench -url https://api.test/login -m POST -H "Content-Type: application/json" -body '{"a":1}' -c 20 -n 2000
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"math"
	"math/bits"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const version = "1.0.0"

// ---------------------------------------------------------------- 参数处理

// headerList 支持 -H 参数重复传入。
type headerList []string

func (h *headerList) String() string { return strings.Join(*h, "; ") }

func (h *headerList) Set(v string) error {
	*h = append(*h, v)
	return nil
}

// Config 存放全部压测参数。
type Config struct {
	URL         string
	Method      string
	Concurrency int
	Requests    int           // 总请求数，0 表示不限（此时需用 -d 控制时长）
	Duration    time.Duration // 压测时长，0 表示不限（此时需用 -n 控制总数）
	QPS         int           // 全局限速，0 表示不限速
	Timeout     time.Duration
	Headers     headerList
	Body        string
	BodyFile    string
	KeepAlive   bool
	Insecure    bool
	DumpLatency string // 延迟原始数据导出路径（CSV）
	Verbose     bool
}

func parseFlags() *Config {
	cfg := &Config{}
	flag.StringVar(&cfg.URL, "url", "", "目标 URL（必填）")
	flag.StringVar(&cfg.Method, "m", "GET", "HTTP 方法")
	flag.IntVar(&cfg.Concurrency, "c", 10, "并发数")
	flag.IntVar(&cfg.Requests, "n", 0, "总请求数，0 表示不限（配合 -d 使用）")
	flag.DurationVar(&cfg.Duration, "d", 0, "压测时长，如 10s / 2m，0 表示不限（配合 -n 使用）")
	flag.IntVar(&cfg.QPS, "qps", 0, "全局限速（每秒请求数），0 表示不限速")
	flag.DurationVar(&cfg.Timeout, "timeout", 10*time.Second, "单次请求超时")
	flag.Var(&cfg.Headers, "H", "自定义请求头，可重复传入，如 -H \"Authorization: Bearer x\"")
	flag.StringVar(&cfg.Body, "body", "", "请求体字符串")
	flag.StringVar(&cfg.BodyFile, "body-file", "", "从文件读取请求体")
	flag.BoolVar(&cfg.KeepAlive, "keepalive", true, "是否启用 HTTP Keep-Alive 连接复用")
	flag.BoolVar(&cfg.Insecure, "insecure", false, "跳过 TLS 证书校验（自签证书场景）")
	flag.StringVar(&cfg.DumpLatency, "dump-latency", "", "将每次请求的延迟导出为 CSV 文件")
	flag.BoolVar(&cfg.Verbose, "v", false, "打印详细错误信息")
	showVer := flag.Bool("version", false, "打印版本号")

	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "y-http-bench v%s - 用 Go 写的 HTTP 压测工具\n\n用法:\n  y-http-bench -url <url> [options]\n\n参数:\n", version)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVer {
		fmt.Printf("y-http-bench v%s\n", version)
		os.Exit(0)
	}

	if cfg.URL == "" {
		flag.Usage()
		os.Exit(2)
	}
	if cfg.Requests == 0 && cfg.Duration == 0 {
		fmt.Fprintln(os.Stderr, "错误：-n 和 -d 至少要指定一个，否则测试不会结束")
		os.Exit(2)
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.BodyFile != "" {
		data, err := os.ReadFile(cfg.BodyFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取 body-file 失败: %v\n", err)
			os.Exit(2)
		}
		cfg.Body = string(data)
	}
	return cfg
}

// ---------------------------------------------------------------- 采样记录

// Limiter 全局限速器（绝对时间表 / 开环调度）。
//
// 注意：这里没有用 time.Ticker —— 在 Windows 上 ticker 的最小间隔受系统定时器
// 精度限制（约 15.6ms），会把吞吐死死卡在 60~90 req/s。这里改成"绝对时间表"式
// 调度：第 n 个请求的理想发出时刻 = start + n×interval，调度只依赖 time.Now()，
// 因此不受定时器最小分辨率约束；落后于时间表时会追赶补发，长期平均速率准确。
//
// Wait 返回该请求的「理想发出时刻」，调用方以它为延迟计时起点：被限速器推迟的
// 排队时间会一并计入延迟，避免 coordinated omission 让高负载下的 P99 偏乐观
// （wrk2 的 fixed-rate 模式是同样的思路）。
type Limiter struct {
	interval time.Duration
	start    time.Time
	seq      int64
}

// NewLimiter 按每秒 qps 个请求的速率创建限速器；qps <= 0 时返回 nil（不限速）。
func NewLimiter(qps int) *Limiter {
	if qps <= 0 {
		return nil
	}
	interval := time.Duration(int64(time.Second) / int64(qps))
	if interval <= 0 {
		interval = time.Nanosecond
	}
	return &Limiter{interval: interval}
}

// Start 设定时间表起点，应在压测真正开始（worker 启动）前调用。
func (l *Limiter) Start(t time.Time) {
	if l != nil {
		l.start = t
	}
}

// Wait 阻塞到本次请求的许可时刻，并返回其理想发出时刻。
func (l *Limiter) Wait() time.Time {
	if l == nil {
		return time.Now()
	}
	n := atomic.AddInt64(&l.seq, 1) - 1
	at := l.start.Add(time.Duration(n) * l.interval)
	if wait := time.Until(at); wait > 0 {
		time.Sleep(wait)
	}
	return at
}

type Record struct {
	Latency time.Duration
	Status  int
	Bytes   int64
	ErrMsg  string // 非空表示本次请求失败（网络层错误）
}

// ---------------------------------------------------------------- 延迟直方图

// Histogram 是 HdrHistogram 的简化实现：对数线性分桶，内存占用与请求量无关。
// 每个「量级」有 subBucketCount 个桶，第 m 量级的桶宽为 2^m 纳秒，相对误差上限
// 约 1/subBucketCount（千分之一），支撑 P99.9 这类分位数绰绰有余。
// 共 maxMagnitudes×subBucketCount 个桶（约 256KB），可表示到约 36 分钟的延迟，
// 超出上限的值一律计入最高桶。
const (
	subBucketBits  = 10
	subBucketCount = 1 << subBucketBits
	maxMagnitudes  = 32
	maxLatencyNS   = (int64(1) << (subBucketBits + maxMagnitudes - 1)) - 1
)

type Histogram struct {
	counts [maxMagnitudes][subBucketCount]int64
	total  int64
	min    int64
	max    int64
	sum    int64
}

// Record 记录一次延迟（纳秒）。
func (h *Histogram) Record(v int64) {
	if v < 0 {
		v = 0
	}
	if v > maxLatencyNS {
		v = maxLatencyNS
	}
	if h.total == 0 || v < h.min {
		h.min = v
	}
	if v > h.max {
		h.max = v
	}
	h.total++
	h.sum += v

	// v < 2^10 时落在量级 0（一值一桶），否则按最高有效位定出量级与桶内偏移。
	mag := 0
	if v >= subBucketCount {
		mag = bits.Len64(uint64(v)) - subBucketBits
	}
	h.counts[mag][v>>uint(mag)]++
}

// ValueAtPercentile 返回第 p 百分位延迟（纳秒），取桶中点近似。
// 结果会被夹到 [min, max]：桶中点可能略高于实测最大值，不夹的话报告里会出现
// P99.9 比 Max 还大的观感问题。
func (h *Histogram) ValueAtPercentile(p float64) int64 {
	if h.total == 0 {
		return 0
	}
	rank := int64(math.Ceil(p / 100 * float64(h.total)))
	if rank < 1 {
		rank = 1
	}
	var cum int64
	for mag := 0; mag < maxMagnitudes; mag++ {
		for sub := 0; sub < subBucketCount; sub++ {
			c := h.counts[mag][sub]
			if c == 0 {
				continue
			}
			if cum += c; cum >= rank {
				return clamp(bucketValue(mag, sub), h.min, h.max)
			}
		}
	}
	return h.max
}

func clamp(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// bucketValue 返回 (mag, sub) 桶的代表值，即桶中点。
func bucketValue(mag, sub int) int64 {
	if mag == 0 {
		return int64(sub)
	}
	return int64(sub)<<uint(mag) + int64(1)<<uint(mag-1)
}

// ---------------------------------------------------------------- 结果汇总

// Collector 汇总各 worker 的采样数据。
//
// 统计口径不保留全量样本：延迟进直方图，状态码、错误、字节数按类累加；
// -dump-latency 则在合并样本时顺手写盘（流式），因此内存占用与请求量无关。
type Collector struct {
	// 实时进度计数：每请求更新一次，用原子操作，只服务于进度显示。
	done     atomic.Int64
	failLive atomic.Int64

	mu      sync.Mutex
	hist    *Histogram
	status  map[int]int
	errs    map[string]int
	records int64         // 记录总数（含失败）
	failed  int64         // 网络层错误 + 状态码 >= 400
	non2xx  int64         // 2xx 以外且 < 400（如 3xx）
	bytes   int64         // 响应体字节数
	dump    *bufio.Writer // 非 nil 时把每条样本流式写入 CSV
}

func newCollector(dump *bufio.Writer) *Collector {
	return &Collector{
		hist:   &Histogram{},
		status: make(map[int]int),
		errs:   make(map[string]int),
		dump:   dump,
	}
}

// merge 合并一批样本：更新统计口径，必要时顺手落盘 CSV。
// 写入错误会由 bufio 累积，在 flushDump 时统一返回。
func (c *Collector) merge(buf []Record) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range buf {
		if c.dump != nil {
			fmt.Fprintf(c.dump, "%.3f,%d,%d,%s\n",
				float64(r.Latency.Microseconds())/1000, r.Status, r.Bytes, r.ErrMsg)
		}
		c.records++
		if r.ErrMsg != "" {
			c.failed++
			c.errs[r.ErrMsg]++
			continue
		}
		c.status[r.Status]++
		switch {
		case r.Status >= 400:
			c.failed++
		case r.Status < 200 || r.Status >= 300:
			c.non2xx++
		}
		c.bytes += r.Bytes
		c.hist.Record(int64(r.Latency))
	}
}

// flushDump 把 CSV 缓冲刷到磁盘；压测结束后调用，此时已无并发写入。
func (c *Collector) flushDump() error {
	if c.dump == nil {
		return nil
	}
	return c.dump.Flush()
}

// openDump 打开 -dump-latency 指定的 CSV 文件，返回行缓冲写入器与关闭函数。
// path 为空时返回空写入器（即不导出）。
func openDump(path string) (*bufio.Writer, func(), error) {
	if path == "" {
		return nil, func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("创建导出文件失败: %w", err)
	}
	w := bufio.NewWriterSize(f, 1<<16)
	if _, err := w.WriteString("latency_ms,status,bytes,error\n"); err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("写入导出表头失败: %w", err)
	}
	return w, func() {
		if err := w.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "导出延迟数据失败: %v\n", err)
		}
		if err := f.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "关闭导出文件失败: %v\n", err)
		}
	}, nil
}

// ---------------------------------------------------------------- 主流程

func main() {
	cfg := parseFlags()

	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run(cfg *Config) error {
	client := newClient(cfg)

	dump, closeDump, err := openDump(cfg.DumpLatency)
	if err != nil {
		return err
	}
	defer closeDump()
	collector := newCollector(dump)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ctrl+C 时优雅停止并输出已采集的报告
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		fmt.Println("\n收到中断信号，停止压测并生成报告...")
		cancel()
	}()

	// 时长控制
	if cfg.Duration > 0 {
		timer := time.NewTimer(cfg.Duration)
		defer timer.Stop()
		go func() {
			select {
			case <-timer.C:
				cancel()
			case <-ctx.Done():
			}
		}()
	}

	// 限速器（全局共享）
	limiter := NewLimiter(cfg.QPS)

	var sent int64
	var wg sync.WaitGroup

	fmt.Printf("开始压测: %s %s | 并发=%d 总请求=%s 时长=%s 限速=%s\n\n",
		cfg.Method, cfg.URL, cfg.Concurrency,
		orUnlimited(cfg.Requests), orZeroDur(cfg.Duration), orUnlimited(cfg.QPS))

	start := time.Now()
	limiter.Start(start) // 时间表起点与计时起点对齐
	stopProgress := startProgress(collector, start)

	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker(ctx, cfg, client, limiter, &sent, collector)
		}()
	}
	wg.Wait()
	stopProgress()

	elapsed := time.Since(start)
	if elapsed <= 0 {
		elapsed = time.Nanosecond
	}

	// 等待超时/取消导致的在途请求已由 worker 收尾。
	if collector.done.Load() == 0 {
		fmt.Println("没有采集到任何样本。")
		return nil
	}

	rep := buildReport(collector)
	rep.Print(cfg, elapsed)

	if dump != nil {
		if err := collector.flushDump(); err != nil {
			fmt.Fprintf(os.Stderr, "导出延迟数据失败: %v\n", err)
		} else {
			fmt.Printf("\n延迟原始数据已导出到: %s\n", cfg.DumpLatency)
		}
	}
	return nil
}

func newClient(cfg *Config) *http.Client {
	return &http.Client{
		Timeout: cfg.Timeout,
		Transport: &http.Transport{
			// 手写 Transport 时 Go 不会自动启用 HTTP/2，必须显式打开：
			// HTTPS 目标会协商到 h2（明文 h2c 不在 net/http 的支持范围内）。
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          cfg.Concurrency * 4,
			MaxIdleConnsPerHost:   cfg.Concurrency * 2,
			MaxConnsPerHost:       cfg.Concurrency * 2,
			IdleConnTimeout:       90 * time.Second,
			DisableKeepAlives:     !cfg.KeepAlive,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: cfg.Timeout,
			ExpectContinueTimeout: time.Second,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: cfg.Insecure},
		},
	}
}

func newRequest(cfg *Config) (*http.Request, error) {
	var body io.Reader
	if cfg.Body != "" {
		body = strings.NewReader(cfg.Body)
	}
	req, err := http.NewRequest(cfg.Method, cfg.URL, body)
	if err != nil {
		return nil, err
	}
	for _, h := range cfg.Headers {
		k, v, ok := strings.Cut(h, ":")
		if !ok {
			return nil, fmt.Errorf("请求头格式错误（缺少冒号）: %q", h)
		}
		req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "y-http-bench/"+version)
	}
	return req, nil
}

// worker 持续发请求直到：次数用尽 / 被取消 / 超时到期。
func worker(ctx context.Context, cfg *Config, client *http.Client, lim *Limiter, sent *int64, col *Collector) {
	// 每个 worker 先把样本攒在本地 buffer，结束时一次性合并，避免高频抢锁。
	buf := make([]Record, 0, 1024)
	defer func() {
		if len(buf) > 0 {
			col.merge(buf)
		}
	}()

	template, err := newRequest(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "构造请求失败: %v\n", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// 总请求数控制
		if cfg.Requests > 0 {
			if int(atomic.AddInt64(sent, 1)) > cfg.Requests {
				return
			}
		} else {
			atomic.AddInt64(sent, 1)
		}

		// 限速：等到本请求的许可时刻，startAt 为其「理想发出时刻」，
		// 落后于时间表所积累的排队时间会由 doRequest 计入延迟。
		startAt := lim.Wait()

		rec := doRequest(ctx, template, client, cfg.Body, startAt)

		// 停止信号到来后被中断的在途请求不计入统计（那不是服务端的错）
		if rec.ErrMsg != "" && ctx.Err() != nil {
			return
		}

		buf = append(buf, rec)
		col.done.Add(1)
		if rec.ErrMsg != "" || rec.Status >= 400 {
			col.failLive.Add(1)
		}
		if len(buf) >= 4096 { // 定期回吐，控制单 worker 内存
			col.merge(buf)
			buf = buf[:0]
		}
	}
}

// doRequest 执行一次请求并返回采样。
// startAt 是延迟计时起点：不限速时为发起时刻，限速时为「理想发出时刻」，
// 这样被限速器推迟的时间也会计入延迟（coordinated omission 修正）。
// 注意：template 的 Body 是共享的 *strings.Reader，绝不能被多个 goroutine 同时读，
// 因此每次请求都用独立的 reader 覆盖 Body。
func doRequest(ctx context.Context, template *http.Request, client *http.Client, body string, startAt time.Time) Record {
	req := template.Clone(ctx)
	if body != "" {
		req.Body = io.NopCloser(strings.NewReader(body))
	}

	resp, err := client.Do(req)
	if err != nil {
		return Record{Latency: time.Since(startAt), ErrMsg: truncate(err.Error(), 120)}
	}
	defer resp.Body.Close()

	n, _ := io.Copy(io.Discard, resp.Body)
	latency := time.Since(startAt)
	return Record{Latency: latency, Status: resp.StatusCode, Bytes: n}
}

// ---------------------------------------------------------------- 终端对齐

// 终端里中日韩文字占 2 个显示列，而 fmt 的 %-8s 是按"字符个数"补空格的，
// 于是中文表头会被撑得比纯数字的数据行更宽，整体错位。下面这组函数按显示宽度补空格。
func runeWidth(r rune) int {
	switch {
	case r < 32: // 控制字符不占位
		return 0
	case r < 0x1100:
		return 1
	case r <= 0x115F, // 谚文字母
		r == 0x2329, r == 0x232A,
		r >= 0x2E80 && r <= 0xA4CF && r != 0x303F, // 中日韩部首、汉字、假名、彝文
		r >= 0xAC00 && r <= 0xD7A3,                // 谚文音节
		r >= 0xF900 && r <= 0xFAFF,                // 中日韩兼容表意文字
		r >= 0xFE30 && r <= 0xFE6F,                // 中日韩兼容形式
		r >= 0xFF00 && r <= 0xFF60,                // 全角字符
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F9FF, // emoji
		r >= 0x20000 && r <= 0x3FFFD: // 扩展汉字
		return 2
	}
	return 1
}

// displayWidth 返回字符串在终端中的显示列数。
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// padRight 左对齐并补空格到指定显示宽度；超宽则原样返回。
func padRight(s string, width int) string {
	if w := displayWidth(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

// ---------------------------------------------------------------- 进度输出

func startProgress(col *Collector, start time.Time) (stop func()) {
	stopCh := make(chan struct{})
	var once sync.Once
	lastDone, lastAt := int64(0), start

	// 列宽按显示宽度对齐：耗时 8 列，其余数字列 10 列（与下方数据行一致）
	const (
		wElapsed = 8
		wDone    = 10
		wQPS     = 10
		wFail    = 6
	)
	fmt.Printf("%s %s %s %s\n",
		padRight("耗时", wElapsed), padRight("已完成", wDone),
		padRight("实时QPS", wQPS), padRight("失败", wFail))
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				now := time.Now()
				done := col.done.Load()
				instQPS := float64(done-lastDone) / now.Sub(lastAt).Seconds()
				fail := col.failLive.Load()
				fmt.Printf("%s %s %s %s\n",
					padRight(now.Sub(start).Round(time.Second).String(), wElapsed),
					padRight(strconv.FormatInt(done, 10), wDone),
					padRight(strconv.FormatFloat(instQPS, 'f', 1, 64), wQPS),
					strconv.FormatInt(fail, 10))
				lastDone, lastAt = done, now
			case <-stopCh:
				return
			}
		}
	}()
	return func() { once.Do(func() { close(stopCh) }) }
}

// ---------------------------------------------------------------- 报告

type Report struct {
	Total     int
	Failed    int
	Non2xx    int
	Bytes     int64
	StatusMap map[int]int
	ErrMap    map[string]int
	Min       time.Duration
	Max       time.Duration
	Avg       time.Duration
	P50       time.Duration
	P90       time.Duration
	P95       time.Duration
	P99       time.Duration
	P999      time.Duration
}

// buildReport 从采集器的直方图与累加计数生成报告（不保留全量样本）。
// 延迟口径与旧版一致：只统计拿到响应的请求，网络层错误只计入失败与错误分布。
func buildReport(col *Collector) *Report {
	col.mu.Lock()
	defer col.mu.Unlock()

	rep := &Report{
		Total:     int(col.records),
		Failed:    int(col.failed),
		Non2xx:    int(col.non2xx),
		Bytes:     col.bytes,
		StatusMap: col.status,
		ErrMap:    col.errs,
	}
	h := col.hist
	if h.total == 0 {
		return rep
	}
	rep.Min = time.Duration(h.min)
	rep.Max = time.Duration(h.max)
	rep.Avg = time.Duration(h.sum / h.total)
	rep.P50 = time.Duration(h.ValueAtPercentile(50))
	rep.P90 = time.Duration(h.ValueAtPercentile(90))
	rep.P95 = time.Duration(h.ValueAtPercentile(95))
	rep.P99 = time.Duration(h.ValueAtPercentile(99))
	rep.P999 = time.Duration(h.ValueAtPercentile(99.9))
	return rep
}

func (r *Report) Print(cfg *Config, elapsed time.Duration) {
	sec := elapsed.Seconds()
	rps := float64(r.Total) / sec
	mb := func(b int64) float64 { return float64(b) / 1024 / 1024 }

	// 标签列统一 12 个显示列，"目标 URL"、"非 2xx" 这类混排标签也能对齐
	const wLabel = 12
	row := func(label, value string) {
		fmt.Printf("%s %s\n", padRight(label, wLabel), value)
	}

	fmt.Printf("\n%s\n", strings.Repeat("=", 58))
	row("目标 URL", cfg.URL)
	row("方法", cfg.Method)
	row("并发数", strconv.Itoa(cfg.Concurrency))
	row("总耗时", elapsed.Round(time.Millisecond).String())
	row("总请求数", strconv.Itoa(r.Total))
	row("失败数", strconv.Itoa(r.Failed))
	if r.Total > 0 {
		row("成功率", fmt.Sprintf("%.2f%%", 100*float64(r.Total-r.Failed)/float64(r.Total)))
	}
	if r.Non2xx > 0 {
		row("非 2xx", strconv.Itoa(r.Non2xx))
	}
	row("平均 QPS", fmt.Sprintf("%.2f req/s", rps))
	if cfg.QPS > 0 {
		row("目标 QPS", strconv.Itoa(cfg.QPS))
		row("速率达成率", fmt.Sprintf("%.2f%%", 100*rps/float64(cfg.QPS)))
	}
	row("吞吐量", fmt.Sprintf("%.2f MB/s (共 %.2f MB)", mb(r.Bytes)/sec, mb(r.Bytes)))
	fmt.Println(strings.Repeat("-", 58))

	fmt.Println("延迟分布（毫秒）:")
	cols := [][2]string{
		{"Min", ms(r.Min)},
		{"Avg", ms(r.Avg)},
		{"P50", ms(r.P50)},
		{"P90", ms(r.P90)},
		{"P95", ms(r.P95)},
		{"P99", ms(r.P99)},
		{"P99.9", ms(r.P999)},
		{"Max", ms(r.Max)},
	}
	for _, c := range cols {
		fmt.Printf("  %-8s %s\n", c[0], c[1])
	}
	fmt.Println(strings.Repeat("-", 58))

	if len(r.StatusMap) > 0 {
		fmt.Println("状态码分布:")
		codes := make([]int, 0, len(r.StatusMap))
		for c := range r.StatusMap {
			codes = append(codes, c)
		}
		sort.Ints(codes)
		for _, c := range codes {
			n := r.StatusMap[c]
			fmt.Printf("  %-8d %-10d (%.2f%%)\n", c, n, 100*float64(n)/float64(r.Total))
		}
		fmt.Println(strings.Repeat("-", 58))
	}

	if len(r.ErrMap) > 0 {
		fmt.Println("错误分布:")
		errs := make([]string, 0, len(r.ErrMap))
		for e := range r.ErrMap {
			errs = append(errs, e)
		}
		sort.Strings(errs)
		limit := len(errs)
		max := 5
		if !cfg.Verbose && limit > max {
			limit = max
		}
		for i := 0; i < limit; i++ {
			e := errs[i]
			fmt.Printf("  %-8d %s\n", r.ErrMap[e], truncate(e, 70))
		}
		if limit < len(errs) {
			fmt.Printf("  ... 其余 %d 类错误（-v 查看全部）\n", len(errs)-limit)
		}
		fmt.Println(strings.Repeat("=", 58))
	}
}

func ms(d time.Duration) string {
	return fmt.Sprintf("%.2f", float64(d.Microseconds())/1000)
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func orUnlimited(n int) string {
	if n <= 0 {
		return "不限"
	}
	return fmt.Sprint(n)
}

func orZeroDur(d time.Duration) string {
	if d <= 0 {
		return "不限"
	}
	return d.String()
}

