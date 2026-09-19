package metrics

import (
	"strings"
	"testing"
)

func TestCounterRendering(t *testing.T) {
	r := New()
	r.IncCounter("devlens_http_requests_total", map[string]string{
		"method": "POST",
		"route":  "/api/v1/incidents/analyze",
		"status": "202",
	})
	r.IncCounter("devlens_http_requests_total", map[string]string{
		"method": "POST",
		"route":  "/api/v1/incidents/analyze",
		"status": "202",
	})

	out := r.Render()
	if !strings.Contains(out, "# TYPE devlens_http_requests_total counter") {
		t.Error("missing TYPE line")
	}
	if !strings.Contains(out, `devlens_http_requests_total{method="POST",route="/api/v1/incidents/analyze",status="202"} 2`) {
		t.Errorf("unexpected counter output:\n%s", out)
	}
}

func TestHistogramRendering(t *testing.T) {
	r := New()
	r.ObserveDuration("devlens_http_request_duration_seconds", 0.02)
	r.ObserveDuration("devlens_http_request_duration_seconds", 3)

	out := r.Render()
	for _, want := range []string{
		"# TYPE devlens_http_request_duration_seconds histogram",
		`le="0.01"`,
		`le="+Inf"`,
		"devlens_http_request_duration_seconds_sum 3.02",
		"devlens_http_request_duration_seconds_count 2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestHistogramBucketsAreCumulative 确认分桶是累计计数。
func TestHistogramBucketsAreCumulative(t *testing.T) {
	r := New()
	// 三次观测，分别落在第一、第二、最后一个桶里。
	r.ObserveDuration("d", 0.005)
	r.ObserveDuration("d", 0.03)
	r.ObserveDuration("d", 100)

	out := r.Render()

	if !strings.Contains(out, `d_bucket{le="0.01"} 1`) {
		t.Errorf(`expected le="0.01" bucket to be 1:\n%s`, out)
	}
	if !strings.Contains(out, `d_bucket{le="0.05"} 2`) {
		t.Errorf(`expected le="0.05" bucket to be 2 (cumulative):\n%s`, out)
	}
	if !strings.Contains(out, `d_bucket{le="+Inf"} 3`) {
		t.Errorf(`expected +Inf bucket to be 3:\n%s`, out)
	}
}

func TestFormatKeySortsLabels(t *testing.T) {
	// 标签顺序不同但内容相同的序列必须合并成一条。
	a := formatKey("m", map[string]string{"b": "2", "a": "1"})
	b := formatKey("m", map[string]string{"a": "1", "b": "2"})
	if a != b {
		t.Errorf("label order should not matter: %q vs %q", a, b)
	}
	if a != `m{a="1",b="2"}` {
		t.Errorf("unexpected key: %q", a)
	}
}

func TestFormatKeyWithoutLabels(t *testing.T) {
	if got := formatKey("m", nil); got != "m" {
		t.Errorf("got %q, want m", got)
	}
}

func TestGauges(t *testing.T) {
	g := NewGauges()
	g.Register("devlens_queue_depth", func() float64 { return 7 })

	out := g.Render()
	if !strings.Contains(out, "# TYPE devlens_queue_depth gauge") {
		t.Errorf("missing TYPE line:\n%s", out)
	}
	if !strings.Contains(out, "devlens_queue_depth 7") {
		t.Errorf("unexpected gauge output:\n%s", out)
	}
}
