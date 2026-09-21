package cache

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache always has an in-process L1. Redis is used as L2 when configured;
// otherwise JSON entries are persisted in .cached so restarts do not cause a
// full cache miss against MongoDB.

var client *redis.Client
var diskEnabled = true
var cacheDir = ".cached"
var diskWrites atomic.Uint64

type memoryValue struct {
	raw       []byte
	expiresAt time.Time
}

var memoryCache = struct {
	sync.RWMutex
	values map[string]memoryValue
}{values: make(map[string]memoryValue)}

// Init connects to Redis from a URL (redis://[:pass@]host:port/db).
// Empty or unavailable Redis enables the local .cached fallback.
func Init(url string) {
	if url == "" {
		enableDiskCache()
		log.Printf("📦 Redis not configured; using local cache at %s", cacheDir)
		return
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		enableDiskCache()
		log.Printf("⚠️ REDIS_URL invalid; using local cache at %s: %v", cacheDir, err)
		return
	}
	c := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		enableDiskCache()
		log.Printf("⚠️ Redis unreachable; using local cache at %s: %v", cacheDir, err)
		return
	}
	client = c
	diskEnabled = false
	log.Printf("📦 Redis cache enabled (%s)", opt.Addr)
}

func Enabled() bool { return client != nil }

// ─── Lookup cache (ค่าเล็กๆ ที่ resolve จาก DB) ───────────────
// ใช้กับ route ที่ body ใหญ่ (เช่น video.m3u8 หลาย KB) — เก็บเฉพาะผล
// lookup ไว้ข้าม DB ส่วนตัว response สร้างสดทุกครั้ง (CF cache ปลายทางแล้ว)

// GetJSON reads key into v. Returns false on miss/error.
func GetJSON(key string, v interface{}) bool {
	if raw, ok := getMemory(key); ok {
		return json.Unmarshal(raw, v) == nil
	}
	var raw []byte
	if client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		var err error
		raw, err = client.Get(ctx, key).Bytes()
		if err != nil {
			return false
		}
	} else if diskEnabled {
		var ok bool
		raw, ok = getDisk(key)
		if !ok {
			return false
		}
	} else {
		return false
	}
	if json.Unmarshal(raw, v) != nil {
		return false
	}
	setMemory(key, raw)
	return true
}

// SetJSON stores v under key with the standard TTL.
func SetJSON(key string, v interface{}) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	setMemory(key, raw)
	if client == nil {
		if diskEnabled {
			setDisk(key, raw)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Set(ctx, key, raw, TTL).Err(); err != nil {
		log.Printf("⚠️ Redis set failed: %v", err)
	}
}

func enableDiskCache() {
	client = nil
	diskEnabled = true
	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		diskEnabled = false
		log.Printf("⚠️ Local cache unavailable: %v", err)
		return
	}
	removeLegacyAssetCacheEntries()
	removeExpiredDiskEntries()
}

func diskPath(key string) string {
	name, ok := diskFileName(key)
	if !ok {
		return ""
	}
	return filepath.Join(cacheDir, name+".json")
}

var safeDiskName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,300}$`)

func diskFileName(key string) (string, bool) {
	for prefix, suffix := range map[string]string{
		"poster_delivery_v2:": "_poster",
		"sprite_delivery_v2:": "_sprite",
	} {
		if strings.HasPrefix(key, prefix) {
			name := strings.TrimPrefix(key, prefix) + suffix
			return name, safeDiskName.MatchString(name)
		}
	}
	for _, prefix := range []string{
		"public_asset_proxy_destination_v3:",
		"playlist_video_v6:",
		"playlist_video_v7:",
		"playlist_video_v8:",
		"playlist_audio_v4:",
		"playlist_audio_v5:",
		"playlist_master_metadata_v1:",
		"playlist_master_metadata_v2:",
		"playlist_master_metadata_v3:",
	} {
		if strings.HasPrefix(key, prefix) {
			name := strings.SplitN(strings.TrimPrefix(key, prefix), ":", 2)[0]
			return name, safeDiskName.MatchString(name)
		}
	}
	return "", false
}

var legacyAssetCacheName = regexp.MustCompile(`^(?:.+_sprite-[0-9]+|.+_poster_(?:poster|[0-9]+))\.json$`)

func removeLegacyAssetCacheEntries() {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() && legacyAssetCacheName.MatchString(entry.Name()) {
			_ = os.Remove(filepath.Join(cacheDir, entry.Name()))
		}
	}
}

func getDisk(key string) ([]byte, bool) {
	filePath := diskPath(key)
	if filePath == "" {
		return nil, false
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, false
	}
	if time.Since(info.ModTime()) >= TTL {
		_ = os.Remove(filePath)
		return nil, false
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, false
	}
	return data, true
}

func setDisk(key string, raw []byte) {
	filePath := diskPath(key)
	if filePath == "" {
		return
	}
	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		return
	}
	temp, err := os.CreateTemp(cacheDir, ".cache-*.tmp")
	if err != nil {
		return
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err = temp.Write(raw); err == nil {
		err = temp.Close()
	} else {
		_ = temp.Close()
	}
	if err == nil {
		if renameErr := os.Rename(tempName, filePath); renameErr != nil {
			// Windows does not replace an existing destination with Rename.
			if os.Remove(filePath) == nil {
				_ = os.Rename(tempName, filePath)
			}
		}
	}
	if diskWrites.Add(1)%256 == 0 {
		removeExpiredDiskEntries()
	}
}

func removeExpiredDiskEntries() {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		filePath := filepath.Join(cacheDir, entry.Name())
		info, statErr := entry.Info()
		if statErr != nil || time.Since(info.ModTime()) >= TTL {
			_ = os.Remove(filePath)
		}
	}
}

// ─── Response cache ──────────────────────────────────────────

// entry is a small cached HTTP response (body is base64 encoded in JSON).
type entry struct {
	Status          int    `json:"s"`
	ContentType     string `json:"ct"`
	ContentLength   string `json:"cl,omitempty"`
	CacheControl    string `json:"cc,omitempty"`
	CDNCacheControl string `json:"cdncc,omitempty"`
	Body            []byte `json:"b"`
}

const maxCacheBody = 512 * 1024 // เกินนี้ไม่ cache (กันของใหญ่หลงเข้ามา)

// recorder จับ response ของ handler จริงไว้ก่อนส่งออก
type recorder struct {
	http.ResponseWriter
	status   int
	body     []byte
	overflow bool
}

func (rec *recorder) WriteHeader(status int) {
	rec.status = status
	rec.ResponseWriter.WriteHeader(status)
}

func (rec *recorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	if !rec.overflow && len(rec.body)+len(b) <= maxCacheBody {
		rec.body = append(rec.body, b...)
	} else {
		rec.body = nil
		rec.overflow = true
	}
	return rec.ResponseWriter.Write(b)
}

// TTL — อายุ cache ทุก route (300s ตามที่ตกลง)
const TTL = 300 * time.Second

// Serve returns a small response from the active cache without calling next.
func Serve(w http.ResponseWriter, r *http.Request, key string, next http.HandlerFunc) {
	if r.Method != http.MethodGet || r.Header.Get("Range") != "" {
		next(w, r)
		return
	}

	var e entry
	if getResponseJSON(key, &e) && e.Status == http.StatusOK {
		if e.ContentType != "" {
			w.Header().Set("Content-Type", e.ContentType)
		}
		if e.ContentLength != "" {
			w.Header().Set("Content-Length", e.ContentLength)
		}
		if e.CacheControl != "" {
			w.Header().Set("Cache-Control", e.CacheControl)
		} else {
			// Backward compatibility for entries written before cache headers
			// became part of the Redis response cache schema.
			w.Header().Set("Cache-Control", "no-store")
		}
		if e.CDNCacheControl != "" {
			w.Header().Set("CDN-Cache-Control", e.CDNCacheControl)
		} else {
			w.Header().Set("CDN-Cache-Control", "public, max-age=300")
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("X-Cache", "HIT")
		w.Write(e.Body)
		return
	}

	rec := &recorder{ResponseWriter: w}
	next(rec, r)

	// เก็บเฉพาะ 200 ที่ body ไม่ใหญ่เกิน — ตอบ client ไปแล้ว เก็บแบบ fire-and-forget
	if rec.status == http.StatusOK && !rec.overflow && len(rec.body) > 0 {
		e := entry{
			Status:          rec.status,
			ContentType:     rec.Header().Get("Content-Type"),
			ContentLength:   rec.Header().Get("Content-Length"),
			CacheControl:    rec.Header().Get("Cache-Control"),
			CDNCacheControl: rec.Header().Get("CDN-Cache-Control"),
			Body:            rec.body,
		}
		setResponseJSON(key, &e)
	}
}

// HTTP response bodies stay in memory (or Redis) and are never written to
// .cached. Disk cache is reserved for small, readable proxy metadata.
func getResponseJSON(key string, v interface{}) bool {
	if raw, ok := getMemory(key); ok {
		return json.Unmarshal(raw, v) == nil
	}
	if client == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	raw, err := client.Get(ctx, key).Bytes()
	if err != nil || json.Unmarshal(raw, v) != nil {
		return false
	}
	setMemory(key, raw)
	return true
}

func setResponseJSON(key string, v interface{}) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	setMemory(key, raw)
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Set(ctx, key, raw, TTL).Err(); err != nil {
		log.Printf("⚠️ Redis set failed: %v", err)
	}
}

func getMemory(key string) ([]byte, bool) {
	memoryCache.RLock()
	value, ok := memoryCache.values[key]
	memoryCache.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(value.expiresAt) {
		memoryCache.Lock()
		delete(memoryCache.values, key)
		memoryCache.Unlock()
		return nil, false
	}
	return append([]byte(nil), value.raw...), true
}

func setMemory(key string, raw []byte) {
	memoryCache.Lock()
	// Keep the process cache bounded during high-cardinality scans.
	if len(memoryCache.values) >= 10_000 {
		memoryCache.values = make(map[string]memoryValue)
	}
	memoryCache.values[key] = memoryValue{
		raw: append([]byte(nil), raw...), expiresAt: time.Now().Add(TTL),
	}
	memoryCache.Unlock()
}
