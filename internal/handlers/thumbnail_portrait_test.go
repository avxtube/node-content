package handlers

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"node-content/internal/cache"
	"testing"
)

func TestPortraitThumbnailRoute(t *testing.T) {
	t.Chdir(t.TempDir())
	source := image.NewNRGBA(image.Rect(0, 0, 660, 336))
	for y := 0; y < 336; y++ {
		for x := 0; x < 660; x++ {
			c := color.NRGBA{R: 255, A: 255}
			if x >= 230 && x < 430 {
				c = color.NRGBA{G: 255, A: 255}
			}
			source.SetNRGBA(x, y, c)
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, source); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Range") != "" {
			t.Error("thumbnail must load complete poster")
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(data.Bytes())
	}))
	defer upstream.Close()
	for _, asset := range []string{"thumb", "thumb-s"} {
		t.Run(asset, func(t *testing.T) {
			slug := "portrait-test-" + asset
			key := fmt.Sprintf("public_asset_proxy_destination_v2:%s:%s:webp:true", slug, asset)
			cache.SetJSON(key, publicAssetLookup{SourceURL: upstream.URL + "/poster.png", Mime: "image/png"})
			parsed, ok := parsePublicFileRequest("/" + slug + "/" + asset + ".webp")
			if !ok || publicFileFilter(parsed)["kind"] != "poster" {
				t.Fatal("wrong poster selection")
			}
			length := ""
			for _, method := range []string{"GET", "HEAD"} {
				r := httptest.NewRequest(method, "/"+slug+"/"+asset+".webp?w=1&h=1&fit=contain", nil)
				r.Header.Set("Range", "bytes=0-9")
				w := httptest.NewRecorder()
				NewHandler(Handler{}).Home(w, r)
				if w.Code != 200 || w.Header().Get("Content-Type") != "image/webp" {
					t.Fatal("thumbnail failed", w.Code)
				}
				if method == "HEAD" {
					if w.Body.Len() != 0 || w.Header().Get("Content-Length") != length {
						t.Fatal("HEAD mismatch")
					}
					continue
				}
				length = w.Header().Get("Content-Length")
				img, _, err := image.Decode(bytes.NewReader(w.Body.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
				width, height := 330, 168
				if asset == "thumb-s" {
					width, height = 180, 320
				}
				if img.Bounds().Dx() != width || img.Bounds().Dy() != height {
					t.Fatal("wrong dimensions", img.Bounds())
				}
				if asset == "thumb-s" {
					red, green, _, _ := img.At(0, height/2).RGBA()
					if green < 50000 || red > 10000 {
						t.Fatal("crop is not centered")
					}
				}
			}
		})
	}
	if _, ok := parsePublicFileRequest("/p/thumb-s.jpg"); ok {
		t.Fatal("non-WebP portrait accepted")
	}
}

func TestPortraitNotFoundDimensions(t *testing.T) {
	for _, method := range []string{"GET", "HEAD"} {
		r := httptest.NewRequest(method, "/missing/thumb-s.webp?w=1&h=1", nil)
		w := httptest.NewRecorder()
		sendNotFound(w, r, http.StatusNotFound)
		if w.Code != 404 || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Content-Length") == "" {
			t.Fatal("wrong placeholder response", w.Code, w.Header())
		}
		if method == "HEAD" {
			if w.Body.Len() != 0 {
				t.Fatal("HEAD body")
			}
			continue
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
		if err != nil || cfg.Width != 180 || cfg.Height != 320 {
			t.Fatal("wrong portrait placeholder", cfg, err)
		}
	}
}
