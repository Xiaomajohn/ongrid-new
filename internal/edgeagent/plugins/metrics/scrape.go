package metrics

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
	"github.com/ongridio/ongrid/internal/edgeagent/plugins"
	"github.com/ongridio/ongrid/internal/edgeagent/plugins/metricscommon"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// specView is the parsed-and-defaulted shape of PluginConfig.Spec.
//
// URLs holds 1..N scrape targets. The plugin loops over them every
// tick and pushes each one's samples in a separate push_prom_samples
// RPC. Single-URL deployments leave len(URLs)==1; the default fans
// out to both node_exporter (:9102) and process_exporter (:9256) so a
// fresh edge produces both host- and process-level series via the
// tunnel without any operator config.
type specView struct {
	URLs        []string
	Interval    time.Duration
	Timeout     time.Duration
	TLSInsecure bool
	BearerToken string
	ExtraLabels map[string]string
	SourceLabel string // value emitted on the wire as PushPromSamplesRequest.Source
}

// Defaults match the host/proc-metrics plugins' subprocesses
// (node_exporter on :9102, process_exporter on :9256 — see
// internal/edgeagent/plugins/hostmetrics, .../procmetrics).
// Localhost only because all processes live in the same systemd unit
// on the edge.
var defaultURLs = []string{
	"http://127.0.0.1:9102/metrics",
	"http://127.0.0.1:9256/metrics",
}

const (
	defaultInterval = 15 * time.Second
	defaultTimeout  = 5 * time.Second
)

// parseSpec reads PluginConfig.Spec into a typed view, applying defaults
// for missing keys. Returns an error only on shapes the operator can fix
// (bad duration string, malformed URL); silently ignores unknown keys.
//
// Three target shapes accepted (first one set wins):
//
//	target_urls: ["http://...", "http://..."]
//	target_url: "http://..." (legacy single-URL form)
//	<missing> → defaultURLs
func parseSpec(spec map[string]interface{}) (specView, error) {
	out := specView{
		URLs:     append([]string(nil), defaultURLs...),
		Interval: defaultInterval,
		Timeout:  defaultTimeout,
	}
	if spec == nil {
		// Empty source label = manager-side Ingester won't attach an
		// `ongrid_source` label, so push samples land looking identical
		// to old direct-scrape series. This is the desired default after
		// retired host.docker.internal scrape — there is
		// only one source now, no need to disambiguate.
		return out, nil
	}

	if urls := stringSlice(spec, "target_urls"); len(urls) > 0 {
		out.URLs = urls
	} else if v := stringFrom(spec, "target_url"); v != "" {
		out.URLs = []string{v}
	}
	if v := stringFrom(spec, "scrape_interval"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return out, fmt.Errorf("scrape_interval %q: %w", v, err)
		}
		if d <= 0 {
			return out, fmt.Errorf("scrape_interval must be > 0; got %v", d)
		}
		out.Interval = d
	}
	if v := stringFrom(spec, "scrape_timeout"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return out, fmt.Errorf("scrape_timeout %q: %w", v, err)
		}
		if d <= 0 {
			return out, fmt.Errorf("scrape_timeout must be > 0; got %v", d)
		}
		out.Timeout = d
	}
	if raw, ok := spec["tls_insecure"]; ok {
		if b, ok := raw.(bool); ok {
			out.TLSInsecure = b
		}
	}
	out.BearerToken = stringFrom(spec, "bearer_token")
	out.ExtraLabels = stringMap(spec, "extra_labels")

	// Prevent obvious misconfig: timeout must not exceed interval, else
	// scrapes overlap themselves and hammer the target.
	if out.Timeout > out.Interval {
		out.Timeout = out.Interval
	}

	// Validate every URL early so bad config surfaces in HealthSnapshot
	// rather than as an HTTP error every tick.
	for _, u := range out.URLs {
		if _, err := url.Parse(u); err != nil {
			return out, fmt.Errorf("target_url %q: %w", u, err)
		}
	}
	// SourceLabel is opt-in via spec.source_label. Default empty so
	// push samples don't carry an ongrid_source label (see comment in
	// the spec==nil branch). Operators can still set one explicitly
	// when running multiple metrics plugins side-by-side.
	if v := stringFrom(spec, "source_label"); v != "" {
		out.SourceLabel = v
	}
	return out, nil
}

// sourceLabelForURL builds the wire-side `source` label. We use
// "metrics:<host>:<port>" so multiple metrics plugins (future) stay
// distinguishable in `ongrid_source` queries.
func sourceLabelForURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "metrics:unknown"
	}
	return "metrics:" + u.Host
}

// scrapeOnce 执行一次 HTTP GET, 解析 Prometheus 文本响应, 返回
// 扁平 sample 切片 + source label. 每个 spec.URL 独立 scrape, 一个
// 200 不会掩盖另一个的失败.
//
// v3.3 起时间戳字段: TsMs = scrape 时刻的 edge 本地 time.Now() (事件时间);
// ServerTimeMs 由 caller (Plugin) 通过 serverTimeMsFn 注入 —
// Plugin 每 tick 从 agent 拿最新心跳响应里的 ServerTimeMs. 注意:
// scrape 内部不钳位本地时间 (v2 的 SafeNow 钳位逻辑已取消, 改在
// manager 端 ingester 用 ServerTimeMs 字段作 Prom sample.timestamp).
func scrapeOnce(ctx context.Context, spec specView, targetURL string, serverTimeMsFn metricscommon.ServerTimeMsFn) ([]tunnel.PromSample, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, spec.SourceLabel, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", string(expfmt.NewFormat(expfmt.TypeTextPlain)))
	if spec.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+spec.BearerToken)
	}
	resp, err := newClient(spec).Do(req)
	if err != nil {
		return nil, spec.SourceLabel, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, spec.SourceLabel, fmt.Errorf("http status %d", resp.StatusCode)
	}

	var p expfmt.TextParser
	families, err := p.TextToMetricFamilies(resp.Body)
	if err != nil {
		return nil, spec.SourceLabel, fmt.Errorf("parse: %w", err)
	}
	mfs := familiesToSlice(families)
	// FlattenSamples 来自 internal/edgeagent/collector — 已经处理
	// counter / gauge / histogram / summary fan-out + extraLabels merge.
	// 我们不重复实现. samples[i].TsMs 由 FlattenSamples 写入 (用 time.Now()),
	// ServerTimeMs 由下面的循环从 serverTimeMsFn 注入 (可为 nil → 留 0).
	samples := collector.FlattenSamples(time.Now(), spec.SourceLabel, mfs, spec.ExtraLabels)
	var serverTimeMs int64
	if serverTimeMsFn != nil {
		serverTimeMs = serverTimeMsFn()
	}
	for i := range samples {
		samples[i].ServerTimeMs = serverTimeMs
	}
	return samples, spec.SourceLabel, nil
}

// newClient builds a per-scrape HTTP client. We don't pool per-target
// because there's typically one scrape target per metrics plugin instance
// and the keep-alive savings don't justify the lifecycle complexity.
func newClient(spec specView) *http.Client {
	tr := &http.Transport{
		MaxIdleConns:        2,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		// Localhost scrape: short dial timeout so the overall scrape
		// timeout is dominated by the read, not by TCP backoff.
		DialContext: (&net.Dialer{
			Timeout: 2 * time.Second,
		}).DialContext,
	}
	if spec.TLSInsecure {
		tr.TLSClientConfig.InsecureSkipVerify = true
	}
	return &http.Client{
		Transport: tr,
		Timeout:   spec.Timeout,
	}
}

// familiesToSlice flattens the (deterministic) name→family map returned
// by expfmt into a slice with stable ordering — keeps tests reproducible.
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

// stringFrom extracts spec[key] as string, tolerating both string and
// fmt-stringable shapes that arrive from JSON decoding (rare for our
// schema but harmless to handle).
func stringFrom(spec map[string]interface{}, key string) string {
	raw, ok := spec[key]
	if !ok {
		return ""
	}
	if s, ok := raw.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// stringSlice extracts a []string from spec[key], tolerating both
// []string and the JSON-decoded []interface{} (whose elements may be
// strings). Empty / wrong-shape returns nil so callers can fall through
// to legacy single-URL form or the default.
func stringSlice(spec map[string]interface{}, key string) []string {
	raw, ok := spec[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	}
	return nil
}

// stringMap extracts a map[string]string from spec[key], tolerating the
// JSON-decoded map[string]interface{} shape.
func stringMap(spec map[string]interface{}, key string) map[string]string {
	raw, ok := spec[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case map[string]string:
		return v
	case map[string]interface{}:
		out := make(map[string]string, len(v))
		for k, val := range v {
			if s, ok := val.(string); ok {
				out[k] = s
			}
		}
		return out
	}
	return nil
}

// Compile-time guard: the Plugin must satisfy plugins.Plugin.
var _ plugins.Plugin = (*Plugin)(nil)
