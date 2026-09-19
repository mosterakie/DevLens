// Package metrics 用 Prometheus 文本格式暴露运行指标。
//
// 没有引入 prometheus/client_golang：需要的指标很少，手写文本格式
// 反而更透明，也省掉一个依赖。等指标变多再换库不迟。
package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Registry 收集计数器和直方图。
type Registry struct {
	mu sync.RWMutex

	// counters 按 "name{labels}" 存储累加值。
	counters map[string]*atomic.Int64
	// histogram 累计每个分桶的观测数。
	histograms map[string]*histogram
}

type histogram struct {
	bounds []float64
	counts []atomic.Int64
	sum    atomic.Int64 // 毫秒总和，避免用浮点原子操作
	count  atomic.Int64
}

// New 构造一个注册表并预置已知指标。
func New() *Registry {
	r := &Registry{
		counters:   map[string]*atomic.Int64{},
		histograms: map[string]*histogram{},
	}
	return r
}

// IncCounter 给某个计数器加一。
func (r *Registry) IncCounter(name string, labels map[string]string) {
	key := formatKey(name, labels)

	r.mu.RLock()
	c, ok := r.counters[key]
	r.mu.RUnlock()

	if !ok {
		r.mu.Lock()
		c, ok = r.counters[key]
		if !ok {
			c = &atomic.Int64{}
			r.counters[key] = c
		}
		r.mu.Unlock()
	}
	c.Add(1)
}

// ObserveDuration 记录一次耗时。
func (r *Registry) ObserveDuration(name string, seconds float64) {
	r.mu.RLock()
	h, ok := r.histograms[name]
	r.mu.RUnlock()

	if !ok {
		r.mu.Lock()
		h, ok = r.histograms[name]
		if !ok {
			// 分桶按秒计。这些边界覆盖了从毫秒级查询到分钟级
			// 分析任务的范围。
			h = &histogram{bounds: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}}
			h.counts = make([]atomic.Int64, len(h.bounds)+1)
			r.histograms[name] = h
		}
		r.mu.Unlock()
	}

	h.sum.Add(int64(seconds * 1000))
	h.count.Add(1)
	for i, b := range h.bounds {
		if seconds <= b {
			h.counts[i].Add(1)
			return
		}
	}
	// 超出最后一个边界。
	h.counts[len(h.bounds)].Add(1)
}

// Render 输出 Prometheus 文本格式。
func (r *Registry) Render() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var b strings.Builder

	names := make([]string, 0, len(r.counters))
	for k := range r.counters {
		names = append(names, k)
	}
	sort.Strings(names)

	for _, k := range names {
		base := k
		if i := strings.IndexByte(k, '{'); i >= 0 {
			base = k[:i]
		}
		fmt.Fprintf(&b, "# TYPE %s counter\n%s %d\n", base, k, r.counters[k].Load())
	}

	hnames := make([]string, 0, len(r.histograms))
	for k := range r.histograms {
		hnames = append(hnames, k)
	}
	sort.Strings(hnames)

	for _, name := range hnames {
		h := r.histograms[name]
		fmt.Fprintf(&b, "# TYPE %s histogram\n", name)

		var cumulative int64
		for i, bound := range h.bounds {
			cumulative += h.counts[i].Load()
			fmt.Fprintf(&b, "%s_bucket{le=\"%g\"} %d\n", name, bound, cumulative)
		}
		cumulative += h.counts[len(h.bounds)].Load()
		fmt.Fprintf(&b, "%s_bucket{le=\"+Inf\"} %d\n", name, cumulative)
		fmt.Fprintf(&b, "%s_sum %g\n", name, float64(h.sum.Load())/1000)
		fmt.Fprintf(&b, "%s_count %d\n", name, h.count.Load())
	}

	return b.String()
}

// SetGauge 直接设置一个仪表值。
//
// 仪表只在渲染时读取，所以单独存放：用 map[string]func() float64
// 让调用方按需计算，避免后台轮询。
type Gauges struct {
	mu     sync.RWMutex
	gauges map[string]func() float64
}

// NewGauges 构造仪表集合。
func NewGauges() *Gauges {
	return &Gauges{gauges: map[string]func() float64{}}
}

// Register 注册一个仪表。
func (g *Gauges) Register(name string, fn func() float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gauges[name] = fn
}

// Render 输出仪表。
func (g *Gauges) Render() string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	names := make([]string, 0, len(g.gauges))
	for k := range g.gauges {
		names = append(names, k)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "# TYPE %s gauge\n%s %g\n", name, name, g.gauges[name]())
	}
	return b.String()
}

// formatKey 把名称与标签拼成 Prometheus 的序列标识。
func formatKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}

	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(name)
	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%s=%q", k, labels[k])
	}
	b.WriteString("}")
	return b.String()
}
