package cache

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func useTestDiskCache(t *testing.T) {
	t.Helper()
	previousDir := cacheDir
	previousDiskEnabled := diskEnabled
	previousClient := client
	cacheDir = filepath.Join(t.TempDir(), ".cached")
	client = nil
	diskEnabled = true
	memoryCache.Lock()
	memoryCache.values = make(map[string]memoryValue)
	memoryCache.Unlock()
	Init("")
	t.Cleanup(func() {
		cacheDir = previousDir
		diskEnabled = previousDiskEnabled
		client = previousClient
		memoryCache.Lock()
		memoryCache.values = make(map[string]memoryValue)
		memoryCache.Unlock()
	})
}

func clearMemoryCache() {
	memoryCache.Lock()
	memoryCache.values = make(map[string]memoryValue)
	memoryCache.Unlock()
}

func TestJSONPersistsToDiskWithoutRedis(t *testing.T) {
	useTestDiskCache(t)

	want := struct {
		SourceURL string `json:"sourceUrl"`
	}{SourceURL: "https://media.example/poster.webp"}
	key := "public_asset_proxy_destination_v2:poster-1:thumb:webp:true"
	SetJSON(key, &want)

	filePath := filepath.Join(cacheDir, "poster-1.json")
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("cache file was not created: %v", err)
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"sourceUrl":"https://media.example/poster.webp"}` {
		t.Fatalf("cache file is not plain JSON: %s", raw)
	}

	clearMemoryCache()
	var got struct {
		SourceURL string `json:"sourceUrl"`
	}
	if !GetJSON(key, &got) {
		t.Fatal("expected disk cache hit after clearing memory")
	}
	if got != want {
		t.Fatalf("disk value = %#v, want %#v", got, want)
	}
}

func TestLookupKeysUseReadableSlugFileNames(t *testing.T) {
	tests := map[string]string{
		"public_asset_proxy_destination_v2:PRKQt1-Ch_ZQS:thumb:webp:true":  "PRKQt1-Ch_ZQS.json",
		"public_asset_proxy_destination_v2:iDtD10-szj_eY:preview:mp4:true": "iDtD10-szj_eY.json",
		"playlist_master_metadata_v1:d3X-U4D_RSPyH":                        "d3X-U4D_RSPyH.json",
		"playlist_video_v6:XMyOmDZE6oS":                                    "XMyOmDZE6oS.json",
		"playlist_audio_v4:MAZeuc0JhmH":                                    "MAZeuc0JhmH.json",
	}
	for key, want := range tests {
		name, ok := diskFileName(key)
		if !ok {
			t.Fatalf("diskFileName(%q) rejected", key)
		}
		if got := name + ".json"; got != want {
			t.Fatalf("diskFileName(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestServeKeepsResponseBodyOutOfDiskCache(t *testing.T) {
	useTestDiskCache(t)

	var calls atomic.Int32
	handler := func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Content-Length", "8")
		_, _ = w.Write([]byte("#EXTM3U\n"))
	}

	first := httptest.NewRecorder()
	Serve(first, httptest.NewRequest(http.MethodGet, "/video.m3u8", nil), "video:1", handler)
	second := httptest.NewRecorder()
	Serve(second, httptest.NewRequest(http.MethodGet, "/video.m3u8", nil), "video:1", handler)

	if calls.Load() != 1 {
		t.Fatalf("handler calls = %d, want 1", calls.Load())
	}
	if second.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("X-Cache = %q, want HIT", second.Header().Get("X-Cache"))
	}
	if second.Header().Get("Content-Length") != "8" {
		t.Fatalf("Content-Length = %q, want 8", second.Header().Get("Content-Length"))
	}
	if second.Body.String() != "#EXTM3U\n" {
		t.Fatalf("cached body = %q", second.Body.String())
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("response body created %d disk cache files", len(entries))
	}
}

func TestServeDoesNotCacheOversizedResponseTail(t *testing.T) {
	useTestDiskCache(t)

	var calls atomic.Int32
	chunk := bytes.Repeat([]byte("x"), maxCacheBody)
	handler := func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write(chunk)
		_, _ = w.Write([]byte("tail"))
	}

	Serve(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/large", nil), "large:1", handler)
	Serve(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/large", nil), "large:1", handler)

	if calls.Load() != 2 {
		t.Fatalf("handler calls = %d, want 2", calls.Load())
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("oversized response created %d disk cache files", len(entries))
	}
}

func TestServeBypassesCachedResponseForRangeRequest(t *testing.T) {
	useTestDiskCache(t)

	var calls atomic.Int32
	handler := func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Range") != "" {
			w.WriteHeader(http.StatusPartialContent)
		}
		_, _ = w.Write([]byte("body"))
	}

	Serve(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/poster.webp", nil), "poster:1", handler)
	rangeRequest := httptest.NewRequest(http.MethodGet, "/poster.webp", nil)
	rangeRequest.Header.Set("Range", "bytes=0-1")
	rangeResponse := httptest.NewRecorder()
	Serve(rangeResponse, rangeRequest, "poster:1", handler)

	if calls.Load() != 2 {
		t.Fatalf("handler calls = %d, want 2", calls.Load())
	}
	if rangeResponse.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want %d", rangeResponse.Code, http.StatusPartialContent)
	}
	if rangeResponse.Header().Get("X-Cache") != "" {
		t.Fatalf("range request unexpectedly used response cache")
	}
}
