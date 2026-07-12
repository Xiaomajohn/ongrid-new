// Package metricscommon contains shared helpers for edge plugins that scrape
// Prometheus exposition endpoints and push the resulting samples through the
// existing tunnel path.
package metricscommon

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"github.com/ongridio/ongrid/internal/edgeagent/collector"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// Target is one HTTP /metrics endpoint to scrape.
type Target struct {
	ID            string
	Name          string
	URL           string
	Enabled       bool
	Interval      time.Duration
	Timeout       time.Duration
	TLSInsecure   bool
	BearerToken   string
	BasicUsername string
	BasicPassword string
	SourceLabel   string
	ExtraLabels   map[string]string
	SampleLimit   int
	LabelDrop     []string
	Kind          string
}

// DefaultInterval is the default scrape interval when callers don't
// supply one (30s). It is also the period the metrics plugin's
// internal ticker falls back to when the operator's spec is missing
// or zero-valued.
const (
	DefaultInterval = 30 * time.Second
	DefaultTimeout  = 5 * time.Second
	// ScrapeUpMetricName mirrors Prometheus's synthetic scrape health
	// metric. Edge-side scrapers push it because these targets are not
	// scraped by the central Prometheus server directly.
	ScrapeUpMetricName = "up"
)

// ServerTimeMsFn 返回 agent 最近一次心跳响应里 manager 端的
// Unix 毫秒时间戳 (trusted 时间源). nil 是合法值 —— helper 会
// 把 PromSample.ServerTimeMs 留为 0, manager 端 ingester 看到
// ServerTimeMs=0 走 legacy fallback (用 TsMs 作 Prom sample.timestamp,
// 不加 edge_ts_ms label).
//
// 这不是 NTP / 时钟同步, 是"以数据时间戳代替系统时钟同步"的设计
// (详见 AGENTS.md 时钟管理硬规则).
type ServerTimeMsFn func() int64

// Scrape 执行一次 GET, 解析 Prometheus 文本响应, 应用 target 侧
// cardinality 控制, 返回扁平 sample 切片.
//
// samples 里的时间戳:
//   - TsMs: edge 本地 time.Now().UnixMilli() (事件时间)
//   - ServerTimeMs: serverTimeMsFn() 的返回值 (ongrid 时间, 0 则表示未就绪)
//
// serverTimeMsFn 可以是 nil (老 variant / 部署初期 agent 未接收到任何
// 带 ServerTimeMs 字段的心跳响应) —— 此时 ServerTimeMs 留 0, manager
// 端 ingester 自动走 legacy fallback, 服务可用但不跱跱 5min hard-reject.
func Scrape(ctx context.Context, target Target, serverTimeMsFn ServerTimeMsFn) ([]tunnel.PromSample, error) {
	if target.URL == "" {
		return nil, fmt.Errorf("target_url required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", string(expfmt.NewFormat(expfmt.TypeTextPlain)))
	if target.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+target.BearerToken)
	}
	if target.BasicUsername != "" || target.BasicPassword != "" {
		req.SetBasicAuth(target.BasicUsername, target.BasicPassword)
	}
	resp, err := httpClient(target).Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	var parser expfmt.TextParser
	families, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	now := time.Now()
	var serverTimeMs int64
	if serverTimeMsFn != nil {
		serverTimeMs = serverTimeMsFn()
	}
	samples := collector.FlattenSamples(now, target.SourceLabel, familiesToSlice(families), target.ExtraLabels)
	// 写入 ongrid 时间戳锚点, manager 端 ingester 会用它作为 Prom 的 sample.timestamp.
	// v3.3 前这里用 SafeNow(serverTime) 钳位本地时间, 避免触发 Prom 的 5min hard-reject;
	// v3.3 后这个职责迁到 ingester (用 ServerTimeMs 字段), edge 只负责搬运, 不钳位.
	for i := range samples {
		samples[i].ServerTimeMs = serverTimeMs
	}
	applyLabelDrop(samples, target.LabelDrop)
	if target.SampleLimit > 0 && len(samples) > target.SampleLimit {
		return nil, fmt.Errorf("sample limit exceeded: got %d limit %d", len(samples), target.SampleLimit)
	}
	return samples, nil
}

// ScrapeUpSample returns the synthetic target availability sample for one
// edge-side scrape. Labels are intentionally limited to low-cardinality source
// metadata; target URL and error text must not become metric labels.
func ScrapeUpSample(now time.Time, plugin string, target Target, up bool) tunnel.PromSample {
	labels := make(map[string]string, len(target.ExtraLabels)+4)
	for k, v := range target.ExtraLabels {
		k = strings.TrimSpace(k)
		if k != "" {
			labels[k] = v
		}
	}
	if plugin != "" {
		labels["plugin"] = plugin
	}
	if target.ID != "" {
		labels["target_id"] = target.ID
	}
	if target.Name != "" {
		labels["target_name"] = target.Name
	}
	if target.Kind != "" {
		labels["kind"] = target.Kind
	}
	value := 0.0
	if up {
		value = 1
	}
	return tunnel.PromSample{
		Name:   ScrapeUpMetricName,
		Labels: labels,
		Value:  value,
		TsMs:   now.UnixMilli(),
	}
}

// ValidateURL checks the target URL shape early during plugin Configure.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("missing host")
	}
	return nil
}

func httpClient(target Target) *http.Client {
	tr := &http.Transport{
		MaxIdleConns:        2,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext: (&net.Dialer{
			Timeout: 2 * time.Second,
		}).DialContext,
	}
	if target.TLSInsecure {
		tr.TLSClientConfig.InsecureSkipVerify = true
	}
	timeout := target.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

func familiesToSlice(in map[string]*dto.MetricFamily) []*dto.MetricFamily {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*dto.MetricFamily, 0, len(keys))
	for _, k := range keys {
		out = append(out, in[k])
	}
	return out
}

func applyLabelDrop(samples []tunnel.PromSample, drops []string) {
	if len(drops) == 0 {
		return
	}
	drop := make(map[string]struct{}, len(drops))
	for _, d := range drops {
		d = strings.TrimSpace(d)
		if d != "" {
			drop[d] = struct{}{}
		}
	}
	if len(drop) == 0 {
		return
	}
	for i := range samples {
		for key := range drop {
			delete(samples[i].Labels, key)
		}
	}
}
