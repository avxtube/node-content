package handlers

import (
	"crypto/tls"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"
)

type upstreamTrace struct {
	mu       sync.Mutex
	DNSMs    float64 `json:"dnsMs"`
	TCPMs    float64 `json:"tcpMs"`
	TLSMs    float64 `json:"tlsMs"`
	Reused   bool    `json:"reused"`
	IdleMs   float64 `json:"idleMs"`
	CFCache  string  `json:"cfCache,omitempty"`
	Age      string  `json:"age,omitempty"`
	CFRay    string  `json:"cfRay,omitempty"`
	Protocol string  `json:"protocol,omitempty"`
	Status   int     `json:"status"`
}

func (t *assetTiming) traceRequest(r *http.Request) *http.Request {
	trace := &upstreamTrace{}
	t.upstream = trace
	var dns, handshake time.Time
	connects := map[string]time.Time{}
	callbacks := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { trace.mu.Lock(); defer trace.mu.Unlock(); dns = time.Now() },
		DNSDone: func(httptrace.DNSDoneInfo) {
			trace.mu.Lock()
			defer trace.mu.Unlock()
			trace.DNSMs += float64(time.Since(dns).Microseconds()) / 1000
		},
		ConnectStart: func(network, addr string) {
			trace.mu.Lock()
			defer trace.mu.Unlock()
			connects[network+addr] = time.Now()
		},
		ConnectDone: func(network, addr string, err error) {
			trace.mu.Lock()
			defer trace.mu.Unlock()
			if start, ok := connects[network+addr]; ok {
				trace.TCPMs += float64(time.Since(start).Microseconds()) / 1000
			}
		},
		TLSHandshakeStart: func() { trace.mu.Lock(); defer trace.mu.Unlock(); handshake = time.Now() },
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			trace.mu.Lock()
			defer trace.mu.Unlock()
			trace.TLSMs += float64(time.Since(handshake).Microseconds()) / 1000
		},
		GotConn: func(info httptrace.GotConnInfo) {
			trace.mu.Lock()
			defer trace.mu.Unlock()
			trace.Reused = info.Reused
			trace.IdleMs = float64(info.IdleTime.Microseconds()) / 1000
		},
	}
	return r.WithContext(httptrace.WithClientTrace(r.Context(), callbacks))
}

func (t *assetTiming) traceResponse(r *http.Response) {
	if t.upstream == nil || r == nil {
		return
	}
	t.upstream.mu.Lock()
	defer t.upstream.mu.Unlock()
	t.upstream.CFCache = r.Header.Get("CF-Cache-Status")
	t.upstream.Age = r.Header.Get("Age")
	t.upstream.CFRay = r.Header.Get("CF-Ray")
	t.upstream.Protocol = r.Proto
	t.upstream.Status = r.StatusCode
}
