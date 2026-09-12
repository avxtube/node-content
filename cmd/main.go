package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"node-content/internal/cache"
	"node-content/internal/config"
	"node-content/internal/db/database"
	"node-content/internal/handlers"
	"node-content/internal/services"
)

// version ถูกฝังตอน build โดย GitHub Actions: -ldflags="-X main.version=v1.x.x"
var version = "dev"

func main() {
	config.Load()
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags)
	log.Printf("🚀 Starting Content Node %s", version)

	// ── MongoDB ───────────────────────────────────────────────
	if err := database.Connect(); err != nil {
		log.Printf("❌ Failed to connect to MongoDB: %v", err)
		time.Sleep(5 * time.Second) // กัน systemd restart-loop รัวๆ
		os.Exit(1)
	}
	defer database.Disconnect()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── Cache: Redis when configured, otherwise local .cached files ──
	cache.Init(config.AppConfig.RedisURL)

	// ── Settings/storage snapshot: load before accepting requests ──
	log.Println("📋 Syncing settings and storages from database...")
	if err := services.SyncSettings(); err != nil {
		log.Printf("⚠️ Initial snapshot sync failed: %v", err)
		if _, loadErr := services.ReadSettingFile(); loadErr != nil {
			log.Printf("⚠️ Existing setting snapshot unavailable: %v", loadErr)
		}
	}
	go services.StartSettingSyncScheduler(ctx)

	// ── Handlers ──────────────────────────────────────────────
	h := handlers.NewHandler(handlers.Handler{})

	// ── HTTP server ───────────────────────────────────────────
	// feeds ครอบ Redis cache — custom domain ที่ไม่ผ่าน CDN ยิงตรงมาบ่อย
	// (vast ไม่ต้อง: อ่านจาก memory sync-cache อยู่แล้ว)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.Health)
	mux.HandleFunc("/ready", h.Health)
	mux.HandleFunc("/vast/", h.Vast)
	mux.HandleFunc("/playlist/", func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/playlist/"), ".json")
		cache.Serve(w, r, "playlist_json_v2:"+r.Host+":"+r.Header.Get("X-Forwarded-Proto")+":"+slug, h.PlaylistJSON)
	})
	mux.HandleFunc("/advert/", func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/advert/"), ".json")
		cache.Serve(w, r, "advert:"+slug, h.AdvertJSON)
	})
	mux.HandleFunc("/", h.Home)

	corsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Range")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		mux.ServeHTTP(w, r)
	})

	server := &http.Server{
		Addr:    ":" + config.AppConfig.Port,
		Handler: corsHandler,
	}

	go func() {
		log.Printf("🌐 Server started at http://localhost:%s", config.AppConfig.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Error starting server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("🛑 Shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("⚠️ Server shutdown failed: %v", err)
	} else {
		log.Println("✅ Server gracefully stopped")
	}
}
