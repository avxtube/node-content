package handlers

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"node-content/internal/cache"
	"strings"
	"testing"
)

func TestAssetTimingHeaderAndCompletionLog(t *testing.T) {
	t.Setenv("ASSET_TIMING", "1")
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	recorder := httptest.NewRecorder()
	timing := newAssetTiming(recorder)
	timing.measure("lookup_cache")()
	timing.WriteHeader(206)
	timing.Write([]byte("clip"))
	timing.measure("stream_body")()
	timing.finish(httptest.NewRequest("GET", "/clip/preview.mp4?secret=omit", nil))
	if recorder.Code != 206 || recorder.Body.String() != "clip" {
		t.Fatal("response changed")
	}
	header := recorder.Result().Header.Get("Server-Timing")
	if !strings.Contains(header, "lookup_cache;dur=") || strings.Contains(header, "stream_body") {
		t.Fatal(header)
	}
	if !strings.Contains(logs.String(), `"bytes":4`) || !strings.Contains(logs.String(), "stream_body") || strings.Contains(logs.String(), "secret") {
		t.Fatal(logs.String())
	}
}

func TestPreviewTimingPreservesRangeStreaming(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=0-3" {
			t.Error("missing Range")
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 0-3/100")
		w.WriteHeader(206)
		io.WriteString(w, "clip")
	}))
	defer upstream.Close()
	cache.SetJSON("public_asset_proxy_destination_v3:timing-preview:preview:mp4:true", publicAssetLookup{SourceURL: upstream.URL, Mime: "video/mp4"})
	r := httptest.NewRequest("GET", "/timing-preview/preview.mp4", nil)
	r.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()
	NewHandler(Handler{}).StreamFile(w, r)
	if w.Code != 206 || w.Body.String() != "clip" || w.Header().Get("Content-Range") != "bytes 0-3/100" {
		t.Fatal("range response changed")
	}
	if !strings.Contains(w.Header().Get("Server-Timing"), "upstream_headers;dur=") {
		t.Fatal("missing upstream timing")
	}
}

func TestPosterTimingWithCachedDelivery(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "poster") }))
	defer upstream.Close()
	cache.SetJSON("poster_delivery_v2:timing-poster", posterDelivery{URLPrefix: upstream.URL + "/", URLSuffix: ".jpg", DurationSecond: 10})
	w := httptest.NewRecorder()
	NewHandler(Handler{}).HandlePoster(w, httptest.NewRequest("GET", "/thumb/timing-poster/poster.jpg", nil))
	if w.Code != 200 || w.Body.String() != "poster" || !strings.Contains(w.Header().Get("Server-Timing"), "upstream_headers;dur=") {
		t.Fatal("poster response or timing missing")
	}
}
