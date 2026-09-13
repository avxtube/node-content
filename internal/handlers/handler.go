package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"node-content/internal/assets"
)

// Handler holds dependencies for HTTP handlers
type Handler struct{}

// NewHandler creates a new Handler instance
func NewHandler(h Handler) *Handler {
	return &h
}

// ─── Not-Found Helpers ────────────────────────────────────────────────────────

// notFoundImage200 holds the pre-resized 200x200 not-found placeholder image
var notFoundImage200 []byte
var notFoundImagePortrait []byte

func init() {
	params := &ImageParams{Width: 200, Height: 200, Fit: "cover", Quality: 80}
	resized, _, err := resizeImage(assets.NotFoundImage, "image/png", params)
	if err != nil {
		notFoundImage200 = assets.NotFoundImage
	} else {
		notFoundImage200 = resized
	}
	notFoundImagePortrait, _, err = resizeImage(assets.NotFoundImage, "image/png", &ImageParams{
		Width: 180, Height: 320, Fit: "cover", Quality: 80, Thumbnail: true,
	})
	if err != nil {
		panic("cannot resize embedded portrait placeholder: " + err.Error())
	}
}

// imageNotFound sends the 200x200 "not found" placeholder PNG
func imageNotFound(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(notFoundImage200)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("CDN-Cache-Control", "public, max-age=60")
	w.WriteHeader(status)
	w.Write(notFoundImage200)
}

// isImagePath checks if the URL path looks like an image request
func isImagePath(path string) bool {
	lower := strings.ToLower(path)
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg", ".ico", ".avif"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// sendNotFound picks the right error format based on the request path:
// - image paths → PNG placeholder
// - everything else → XML NoSuchKey
func sendNotFound(w http.ResponseWriter, r *http.Request, status int) {
	if strings.HasSuffix(strings.ToLower(r.URL.Path), "/thumb-s.webp") {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(notFoundImagePortrait)))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("CDN-Cache-Control", "public, max-age=60")
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = w.Write(notFoundImagePortrait)
		}
		return
	}
	if isImagePath(r.URL.Path) {
		imageNotFound(w, status)
	} else {
		HandleCachedError(w, r, status)
	}
}

// ─── Router ───────────────────────────────────────────────────────────────────
// ครบทุกเส้นของ server-content เดิม: playlist / video / sprite / thumb / stream
// (/vast/ แยกไปเป็น mux route ใน cmd/main.go เหมือนเดิม)

// Home dispatches requests to the appropriate handler
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if path == "/" {
		HandleNotFound(w, r)
		return
	}

	switch {
	// Cache playlists because playback clients request them repeatedly.
	case strings.HasSuffix(path, "/playlist.m3u8"):
		h.HandlePlaylist(w, r)
	case strings.HasSuffix(path, "/video.m3u8"):
		h.HandleVideo(w, r)
	case strings.HasSuffix(path, "/audio.m3u8"):
		h.HandleAudio(w, r)
	case strings.HasSuffix(path, "/subtitle.vtt"):
		h.HandleSubtitle(w, r)
	case strings.HasSuffix(path, "/sprite/sprite.vtt"):
		h.HandleSpriteVTT(w, r)
	case strings.Contains(path, "/sprite/") && strings.HasSuffix(path, ".jpg"):
		h.HandleSpriteImage(w, r)
	case strings.HasPrefix(path, "/thumb/") && strings.HasSuffix(path, ".jpg"):
		h.HandlePoster(w, r)
	default:
		h.StreamFile(w, r)
	}
}
