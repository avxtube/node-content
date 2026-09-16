package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"node-content/internal/cache"
	"testing"
)

func TestShortFileSelection(t *testing.T) {
	r, ok := parsePublicFileRequest("/cb-VP_EKRPPBO/short.mp4")
	if !ok {
		t.Fatal("short rejected")
	}
	f := publicFileFilter(r)
	if f["slug"] != "cb-VP_EKRPPBO" || f["kind"] != "stream" {
		t.Fatal("wrong file selection", f)
	}
	for _, p := range []string{"/x/short.m3u8", "/x/short.jpg", "/x/short.exe"} {
		if _, ok := parsePublicFileRequest(p); ok {
			t.Fatal("unsupported short", p)
		}
	}
}
func TestShortProxyRangeAndHead(t *testing.T) {
	t.Chdir(t.TempDir())
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2026-09-13/short.mp4" {
			t.Error("wrong object")
		}
		if r.Header.Get("Range") != "bytes=2-5" {
			t.Error("Range missing")
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 2-5/10")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", "4")
		w.WriteHeader(206)
		if r.Method != "HEAD" {
			_, _ = w.Write([]byte("2345"))
		}
	}))
	defer upstream.Close()
	slug := "short-range-test"
	key := fmt.Sprintf("public_asset_proxy_destination_v3:%s:short:mp4:true", slug)
	cache.SetJSON(key, publicAssetLookup{SourceURL: upstream.URL + "/2026-09-13/short.mp4", Mime: "video/mp4"})
	h := NewHandler(Handler{})
	for _, method := range []string{"GET", "HEAD"} {
		r := httptest.NewRequest(method, "/"+slug+"/short.mp4", nil)
		r.Header.Set("Range", "bytes=2-5")
		w := httptest.NewRecorder()
		h.Home(w, r)
		if w.Code != 206 || w.Header().Get("Content-Range") != "bytes 2-5/10" || w.Header().Get("Content-Type") != "video/mp4" {
			t.Fatal("bad response", w.Code, w.Header())
		}
		if method == "GET" && w.Body.String() != "2345" {
			t.Fatal("body mismatch")
		}
		if method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD body")
		}
	}
}
