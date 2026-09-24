package handlers

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestThumbnailCacheAndConcurrentRequests(t *testing.T) {
	t.Setenv("THUMB_CACHE_DIR", t.TempDir())
	var pngData bytes.Buffer
	png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 32, 32)))
	var count atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("CF-Cache-Status", "HIT")
		w.Header().Set("Age", "20")
		w.Header().Set("CF-Ray", "test-ray")
		w.Write(pngData.Bytes())
	}))
	defer upstream.Close()
	params := &ImageParams{Width: 32, Height: 32, Fit: "cover", WebP: true, Thumbnail: true, Quality: 80}
	request := func(method string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		timing := newAssetTiming(w)
		NewHandler(Handler{}).serveCachedThumbnail(timing, httptest.NewRequest(method, "/test/thumb.webp", nil), upstream.URL, params, timing)
		if w.Code != 200 {
			t.Errorf("status %d", w.Code)
		}
		if timing.upstream != nil && timing.upstream.CFCache != "HIT" {
			t.Error("missing upstream metadata")
		}
		return w
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); request("GET") }()
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatalf("upstream requests=%d", count.Load())
	}
	head := request("HEAD")
	if head.Body.Len() != 0 || head.Header().Get("X-Image-Cache") != "HIT" || count.Load() != 1 {
		t.Fatal("HEAD must reuse disk cache")
	}
	get := request("GET")
	if get.Body.Len() == 0 || get.Header().Get("Content-Type") != "image/webp" {
		t.Fatal("invalid cached image")
	}
}

func TestThumbnailDiskRejectsCollisionExpiredAndOversize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "0000.json")
	if err := writeThumbnail(path, "one", []byte("test")); err != nil {
		t.Fatal(err)
	}
	if _, ok := readThumbnail(path, "two"); ok {
		t.Fatal("collision returned wrong image")
	}
	raw, _ := json.Marshal(thumbnailEntry{"one", time.Now().Add(-time.Hour), []byte("old")})
	os.WriteFile(path, raw, 0600)
	if _, ok := readThumbnail(path, "one"); ok {
		t.Fatal("expired image served")
	}
	if err := writeThumbnail(path, "one", make([]byte, thumbnailEntryLimit)); err == nil {
		t.Fatal("oversize cache entry accepted")
	}
}

func TestThumbnailFailureIsNotCached(t *testing.T) {
	t.Setenv("THUMB_CACHE_DIR", t.TempDir())
	var count atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); w.WriteHeader(404) }))
	defer upstream.Close()
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		timing := newAssetTiming(w)
		NewHandler(Handler{}).serveCachedThumbnail(timing, httptest.NewRequest("GET", "/test/thumb.webp", nil), upstream.URL, &ImageParams{WebP: true}, timing)
		if w.Code != 404 || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("error was made cacheable")
		}
	}
	if count.Load() != 2 {
		t.Fatal("failure cached")
	}
}
