package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"
)

// Fixed slots bound disk usage without scanning the directory in requests.
// Full-key verification makes collisions a cache miss, never a wrong image.
const thumbnailSlots = 1024
const thumbnailEntryLimit = 256 * 1024
const thumbnailTTL = 24 * time.Hour

type thumbnailEntry struct {
	Key     string
	Expires time.Time
	Body    []byte
}

type thumbnailUpstreamError struct{ status int }

func (e thumbnailUpstreamError) Error() string {
	return fmt.Sprintf("thumbnail upstream status %d", e.status)
}

var thumbnailFlights singleflight.Group
var thumbnailWorkers = make(chan struct{}, 8)

func thumbnailCacheDir() string {
	if dir := os.Getenv("THUMB_CACHE_DIR"); dir != "" {
		return dir
	}
	return ".thumbnail-cache"
}

func thumbnailKey(source string, params *ImageParams) (string, string) {
	raw, _ := json.Marshal(struct {
		Version int
		Source  string
		Params  *ImageParams
	}{1, source, params})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), fmt.Sprintf("%04d.json", binary.BigEndian.Uint32(sum[:4])%thumbnailSlots)
}

func readThumbnail(path, key string) ([]byte, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, thumbnailEntryLimit+1))
	if err != nil || len(raw) > thumbnailEntryLimit {
		return nil, false
	}
	var entry thumbnailEntry
	if json.Unmarshal(raw, &entry) != nil || entry.Key != key || time.Now().After(entry.Expires) || len(entry.Body) == 0 {
		return nil, false
	}
	return entry.Body, true
}

func writeThumbnail(path, key string, body []byte) error {
	raw, err := json.Marshal(thumbnailEntry{key, time.Now().Add(thumbnailTTL), body})
	if err != nil {
		return err
	}
	if len(raw) > thumbnailEntryLimit {
		return fmt.Errorf("thumbnail exceeds cache entry limit")
	}
	if err = os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".thumb-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (h *Handler) serveCachedThumbnail(w http.ResponseWriter, r *http.Request, source string, params *ImageParams, timing *assetTiming) {
	key, slot := thumbnailKey(source, params)
	cachePath := filepath.Join(thumbnailCacheDir(), slot)
	done := timing.measure("image_cache_read")
	body, hit := readThumbnail(cachePath, key)
	done()
	state := "HIT"
	if !hit {
		state = "MISS"
		// Singleflight shares bytes even if disk is unavailable; failed work is never cached.
		value, err, shared := thumbnailFlights.Do(cachePath+":"+key, func() (interface{}, error) {
			if body, ok := readThumbnail(cachePath, key); ok {
				return body, nil
			}
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			done := timing.measure("image_queue")
			select {
			case thumbnailWorkers <- struct{}{}:
			case <-ctx.Done():
				done()
				return nil, ctx.Err()
			}
			done()
			defer func() { <-thumbnailWorkers }()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
			if err != nil {
				return nil, err
			}
			done = timing.measure("upstream_headers")
			resp, err := http.DefaultClient.Do(timing.traceRequest(req))
			timing.traceResponse(resp)
			done()
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return nil, thumbnailUpstreamError{resp.StatusCode}
			}
			done = timing.measure("upstream_body")
			raw, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024+1))
			done()
			if err != nil {
				return nil, err
			}
			if len(raw) > 20*1024*1024 {
				return nil, fmt.Errorf("thumbnail source exceeds 20 MiB")
			}
			done = timing.measure("image_transform")
			config, _, err := image.DecodeConfig(bytes.NewReader(raw))
			if err != nil {
				done()
				return nil, err
			}
			if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40_000_000 {
				done()
				return nil, fmt.Errorf("thumbnail dimensions exceed limit")
			}
			body, _, err := resizeImage(raw, resp.Header.Get("Content-Type"), params)
			done()
			if err != nil {
				return nil, err
			}
			writeDone := timing.measure("image_cache_write")
			if err := writeThumbnail(cachePath, key, body); err != nil {
				timing.copyError = "thumbnail cache write: " + err.Error()
			}
			writeDone()
			return body, nil
		})
		if shared {
			state = "SHARED"
		}
		if err != nil {
			timing.copyError = "thumbnail load failed"
			w.Header().Set("X-Image-Cache", "MISS")
			status := http.StatusBadGateway
			if upstream, ok := err.(thumbnailUpstreamError); ok && upstream.status == http.StatusNotFound {
				status = http.StatusNotFound
			}
			sendNotFound(w, r, status)
			return
		}
		body = value.([]byte)
	}
	w.Header().Set("X-Image-Cache", state)
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Cache-Control", "public, max-age=63072000, immutable")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}
