package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Headers contain only work completed before the first response byte. Streaming
// body time is logged at completion, without buffering videos to measure it.
type assetTiming struct {
	http.ResponseWriter
	started   time.Time
	stages    map[string]float64
	order     []string
	status    int
	bytes     int64
	copyError string
	upstream  *upstreamTrace
}

func newAssetTiming(w http.ResponseWriter) *assetTiming {
	return &assetTiming{ResponseWriter: w, started: time.Now(), stages: make(map[string]float64)}
}

func (t *assetTiming) measure(name string) func() {
	start := time.Now()
	return func() {
		if _, exists := t.stages[name]; !exists {
			t.order = append(t.order, name)
		}
		t.stages[name] += float64(time.Since(start).Microseconds()) / 1000
	}
}

func (t *assetTiming) Unwrap() http.ResponseWriter { return t.ResponseWriter }

func (t *assetTiming) WriteHeader(status int) {
	if t.status != 0 {
		return
	}
	if status >= 100 && status < 200 {
		t.ResponseWriter.WriteHeader(status)
		return
	}
	t.status = status
	parts := make([]string, 0, len(t.order)+1)
	if t.upstream != nil {
		t.upstream.mu.Lock()
		parts = append(parts, fmt.Sprintf("dns;dur=%.2f, tcp;dur=%.2f, tls;dur=%.2f", t.upstream.DNSMs, t.upstream.TCPMs, t.upstream.TLSMs))
		t.upstream.mu.Unlock()
	}
	for _, name := range t.order {
		parts = append(parts, fmt.Sprintf("%s;dur=%.2f", name, t.stages[name]))
	}
	parts = append(parts, fmt.Sprintf("origin_headers;dur=%.2f", float64(time.Since(t.started).Microseconds())/1000))
	t.Header().Add("Server-Timing", strings.Join(parts, ", "))
	t.ResponseWriter.WriteHeader(status)
}

func (t *assetTiming) Write(p []byte) (int, error) {
	if t.status == 0 {
		t.WriteHeader(http.StatusOK)
	}
	n, err := t.ResponseWriter.Write(p)
	t.bytes += int64(n)
	return n, err
}

func (t *assetTiming) finish(r *http.Request) {
	elapsed := time.Since(t.started)
	if os.Getenv("ASSET_TIMING") != "1" && elapsed < time.Second && t.status < 400 && t.copyError == "" {
		return
	}
	if t.upstream != nil {
		t.upstream.mu.Lock()
		defer t.upstream.mu.Unlock()
	}
	data, _ := json.Marshal(map[string]interface{}{
		"path": r.URL.Path, "method": r.Method, "status": t.status,
		"lookupCache": t.Header().Get("X-Lookup-Cache"), "stagesMs": t.stages,
		"totalMs": float64(elapsed.Microseconds()) / 1000, "bytes": t.bytes,
		"streamError": t.copyError,
		"upstream":    t.upstream, "imageCache": t.Header().Get("X-Image-Cache"),
	})
	log.Printf("[asset-timing] %s", data)
}
