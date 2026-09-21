package handlers

import (
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"node-content/internal/cache"
	"node-content/internal/core/enums"
	"node-content/internal/db/models"

	"go.mongodb.org/mongo-driver/bson"
)

// HandlePoster handles GET /thumb/{fileSlug}/{n}.jpg and /thumb/{fileSlug}/poster.jpg.
// Proxies thumbnail from nginx-vod-module via storage
func (h *Handler) HandlePoster(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/thumb/")
	if path == "" {
		sendNotFound(w, r, http.StatusNotFound)
		return
	}

	lastSlash := strings.LastIndex(path, "/")
	if lastSlash <= 0 {
		sendNotFound(w, r, http.StatusNotFound)
		return
	}

	slug := path[:lastSlash]
	filename := path[lastSlash+1:]

	if !strings.HasSuffix(filename, ".jpg") {
		sendNotFound(w, r, http.StatusNotFound)
		return
	}
	timePart := strings.TrimSuffix(filename, ".jpg")
	if timePart == "" {
		sendNotFound(w, r, http.StatusNotFound)
		return
	}
	isDefaultPoster := timePart == "poster"
	if !isDefaultPoster {
		for _, c := range timePart {
			if c < '0' || c > '9' {
				sendNotFound(w, r, http.StatusNotFound)
				return
			}
		}
	}

	if strings.Contains(slug, "/") {
		sendNotFound(w, r, http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	cacheKey := "poster_delivery_v2:" + slug
	var delivery posterDelivery
	if cache.GetJSON(cacheKey, &delivery) && delivery.URLPrefix != "" {
		w.Header().Set("X-Lookup-Cache", "HIT")
	} else {
		w.Header().Set("X-Lookup-Cache", "MISS")
		resolved, err := resolvePosterDelivery(ctx, slug)
		if err != nil {
			log.Printf("[Poster] Cannot resolve delivery for %s: %v", slug, err)
			sendNotFound(w, r, http.StatusNotFound)
			return
		}
		delivery = resolved
		cache.SetJSON(cacheKey, &delivery)
	}
	thumbURL, err := delivery.imageURL(timePart, isDefaultPoster)
	if err != nil {
		sendNotFound(w, r, http.StatusNotFound)
		return
	}
	log.Printf("[Poster] Fetching poster: %s", thumbURL)

	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodGet, thumbURL, nil)
	if err != nil {
		sendNotFound(w, r, http.StatusNotFound)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		log.Printf("[Poster] Upstream request failed: %s → %v", thumbURL, err)
		sendNotFound(w, r, http.StatusNotFound)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[Poster] Upstream returned %d: %s", resp.StatusCode, thumbURL)
		sendNotFound(w, r, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)

	buf := make([]byte, 32*1024)
	io.CopyBuffer(w, resp.Body, buf)
}

type posterDelivery struct {
	Delivery       string  `json:"delivery"`
	URLPrefix      string  `json:"urlPrefix"`
	URLSuffix      string  `json:"urlSuffix"`
	DurationSecond float64 `json:"durationSeconds,omitempty"`
}

func (delivery posterDelivery) imageURL(timePart string, isDefault bool) (string, error) {
	second := 0
	if isDefault {
		second = int(delivery.DurationSecond / 2)
	} else {
		parsed, err := strconv.Atoi(timePart)
		if err != nil || parsed < 0 {
			return "", fmt.Errorf("invalid poster second")
		}
		second = parsed
	}
	if delivery.DurationSecond > 0 && float64(second) >= delivery.DurationSecond {
		second = int(math.Ceil(delivery.DurationSecond)) - 1
		if second < 0 {
			second = 0
		}
	}
	return delivery.URLPrefix + strconv.Itoa(second) + "000" + delivery.URLSuffix, nil
}

func resolvePosterDelivery(ctx context.Context, slug string) (posterDelivery, error) {
	var file models.File
	err := models.FileModel.Col().FindOne(ctx, bson.M{
		"slug": slug, "kind": "stream", "status": bson.M{"$in": playableFileStatuses()},
	}).Decode(&file)
	if err != nil {
		return posterDelivery{}, fmt.Errorf("file not found: %w", err)
	}

	if file.IsTrashed() || file.IsDeleted() {
		return posterDelivery{}, fmt.Errorf("file is deleted")
	}

	// ─── Step 2: Find video media ────────────────────────────────────────
	mediaCursor, err := models.MediaModel.Col().Find(ctx, bson.M{
		"fileId": file.ID,
		"type":   enums.MediaTypeVideo,
	})
	if err != nil {
		return posterDelivery{}, fmt.Errorf("video media lookup: %w", err)
	}
	defer mediaCursor.Close(ctx)

	var videoMedias []models.Media
	for mediaCursor.Next(ctx) {
		var candidate models.Media
		if err := mediaCursor.Decode(&candidate); err == nil {
			videoMedias = append(videoMedias, candidate)
		}
	}
	if err := mediaCursor.Err(); err != nil {
		return posterDelivery{}, fmt.Errorf("video media cursor: %w", err)
	}

	media, ok := selectPosterMedia(videoMedias)
	if !ok {
		return posterDelivery{}, fmt.Errorf("video media not found for fileId=%s", file.ID)
	}

	// ─── Step 3: Find storage ────────────────────────────────────────────
	storageID := strings.TrimSpace(media.StorageID)

	storage, ok := getOnlineStorage(storageID)
	if !ok {
		return posterDelivery{}, fmt.Errorf("storage not found: %s", storageID)
	}
	if storage.IsProxy() {
		return posterDelivery{}, fmt.Errorf("proxy delivery does not generate poster thumbnails")
	}

	vodBaseURL := storage.GetVODBaseURL()
	if vodBaseURL == "" {
		return posterDelivery{}, fmt.Errorf("storage has no VOD URL: %s", storage.ID)
	}
	duration := 0.0
	if file.Metadata != nil && file.Metadata.Duration != nil && *file.Metadata.Duration > 0 {
		duration = *file.Metadata.Duration
	}
	return posterDelivery{
		Delivery: storage.Provider, URLPrefix: fmt.Sprintf("%s/%s/thumb-", vodBaseURL, media.Slug),
		URLSuffix: "-w500.jpg", DurationSecond: duration,
	}, nil
}

// selectPosterMedia chooses the lowest numeric resolution available. Original
// is used only when no processed numeric resolution exists.
func selectPosterMedia(medias []models.Media) (models.Media, bool) {
	var lowest models.Media
	var original models.Media
	lowestResolution := int(^uint(0) >> 1)
	hasLowest := false
	hasOriginal := false

	for _, media := range medias {
		if media.Quality == nil {
			continue
		}
		resolution := strings.TrimSpace(*media.Quality)
		if resolution == enums.ResolutionOriginal {
			original = media
			hasOriginal = true
			continue
		}
		numeric, err := strconv.Atoi(resolution)
		if err == nil && numeric > 0 && numeric < lowestResolution {
			lowest = media
			lowestResolution = numeric
			hasLowest = true
		}
	}

	if hasLowest {
		return lowest, true
	}
	return original, hasOriginal
}

func resolvePosterSecond(timePart string, isDefault bool, file models.File, media models.Media) int {
	duration := 0.0
	if file.Metadata != nil && file.Metadata.Duration != nil && *file.Metadata.Duration > 0 {
		duration = *file.Metadata.Duration
	}

	second := 0
	if isDefault {
		second = int(duration / 2)
	} else if parsed, err := strconv.Atoi(timePart); err == nil && parsed > 0 {
		second = parsed
	}

	if duration > 0 && float64(second) >= duration {
		second = int(math.Ceil(duration)) - 1
		if second < 0 {
			second = 0
		}
	}
	return second
}
