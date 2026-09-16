package handlers

import (
	"bytes"
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"node-content/internal/cache"
	"node-content/internal/db/models"
	"reflect"
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
	// Original image routes must remain byte-for-byte proxies, even for short owners.
	for _, asset := range []string{"poster", "avatar", "cover"} {
		slug := "original-" + asset
		cache.SetJSON(fmt.Sprintf("public_asset_proxy_destination_v3:%s:%s:png:true", slug, asset),
			publicAssetLookup{SourceURL: upstream.URL, Mime: "image/png", FileKind: asset, ContentKind: "short"})
		w := httptest.NewRecorder()
		NewHandler(Handler{}).Home(w, httptest.NewRequest("GET", "/"+slug+"/"+asset+".png", nil))
		if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), data.Bytes()) {
			t.Fatalf("%s must preserve original bytes", asset)
		}
	}
	for _, tc := range []struct {
		name, asset, fileKind, contentKind string
		width, height                      int
	}{
		{"default", "thumb", "poster", "", 330, 168},
		{"video", "thumb", "poster", "video", 330, 168},
		{"post", "thumb", "poster", "post", 330, 168},
		{"short", "thumb", "poster", "short", 180, 320},
		{"avatar", "thumb", "avatar", "short", 200, 200},
		{"cover", "thumb", "cover", "short", 330, 168},
		{"portrait", "thumb-s", "poster", "video", 180, 320},
		{"portrait-avatar", "thumb-s", "avatar", "", 180, 320},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asset := tc.asset
			slug := "portrait-test-" + tc.name
			key := fmt.Sprintf("public_asset_proxy_destination_v3:%s:%s:webp:true", slug, asset)
			cache.SetJSON(key, publicAssetLookup{SourceURL: upstream.URL + "/poster.png", Mime: "image/png", FileKind: tc.fileKind, ContentKind: tc.contentKind})
			parsed, ok := parsePublicFileRequest("/" + slug + "/" + asset + ".webp")
			if !ok || !reflect.DeepEqual(publicFileFilter(parsed)["kind"], bson.M{"$in": []string{"poster", "avatar", "cover"}}) {
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
				width, height := tc.width, tc.height
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

func TestThumbnailContentOwner(t *testing.T) {
	ownerType, ownerID := "content", "content-1"
	file := models.File{Kind: "poster", OwnerType: &ownerType, OwnerID: &ownerID}
	for _, asset := range []string{"poster", "avatar", "cover", "thumb-s", "preview", "short", "thumb"} {
		want := ""
		if asset == "thumb" {
			want = ownerID
		}
		if got := thumbnailContentOwner(publicFileRequest{Asset: asset}, file); got != want {
			t.Fatalf("%s: got %q want %q", asset, got, want)
		}
	}
	for _, kind := range []string{"avatar", "cover"} {
		file.Kind = kind
		if thumbnailContentOwner(publicFileRequest{Asset: "thumb"}, file) != "" {
			t.Fatalf("%s should not look up contents", kind)
		}
		parsed, ok := parsePublicFileRequest("/profile/" + kind + ".webp")
		if !ok || publicFileFilter(parsed)["kind"] != kind {
			t.Fatalf("invalid %s route", kind)
		}
	}
	file.Kind = "poster"
	ownerType = "user"
	if thumbnailContentOwner(publicFileRequest{Asset: "thumb"}, file) != "" {
		t.Fatal("user must not query contents")
	}
	file.OwnerType = nil
	file.OwnerID = nil
	if thumbnailContentOwner(publicFileRequest{Asset: "thumb"}, file) != "" {
		t.Fatal("missing owner must use default")
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
