package handlers

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"node-content/internal/cache"
	"node-content/internal/core/enums"
	"node-content/internal/db/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type publicFileRequest struct {
	Slug      string
	Asset     string
	Extension string
	Nested    bool
}

type publicAssetLookup struct {
	SourceURL string `json:"sourceUrl"`
	Mime      string `json:"mime,omitempty"`
}

// StreamFile handles the current /{fileSlug}/{asset}.{ext} routes and legacy
// /{fileSlug}.{ext} image routes.
// File flow: file.slug → file._id → media by fileId → storage → object.
// Direct image flow: media.slug → image media → storage → object.
func (h *Handler) StreamFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	requested, ok := parsePublicFileRequest(r.URL.Path)
	if !ok {
		sendNotFound(w, r, http.StatusBadRequest)
		return
	}
	fileSlug := requested.Slug

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var media models.Media
	var file models.File
	var sourceURL string
	var mediaMime string
	lookupKey := fmt.Sprintf("public_asset_proxy_destination_v2:%s:%s:%s:%t", requested.Slug, requested.Asset, requested.Extension, requested.Nested)
	var lookup publicAssetLookup
	if cache.GetJSON(lookupKey, &lookup) && lookup.SourceURL != "" {
		sourceURL = lookup.SourceURL
		mediaMime = lookup.Mime
		w.Header().Set("X-Lookup-Cache", "HIT")
	} else {
		w.Header().Set("X-Lookup-Cache", "MISS")

		// ─── Step 1: Find file by slug ───────────────────────────────────
		fileFilter := publicFileFilter(requested)
		err := models.FileModel.Col().FindOne(ctx, fileFilter).Decode(&file)
		if err == nil {
			if file.IsTrashed() || file.IsDeleted() {
				sendNotFound(w, r, http.StatusGone)
				return
			}

			// ─── Step 2a: Find media by fileId == file._id ───────────────
			mediaFilter := bson.M{
				"fileId": file.ID,
			}
			if requested.Nested && (requested.Asset == "preview" || requested.Asset == "short") {
				mediaFilter["type"] = enums.MediaTypeVideo
			} else if requested.Nested {
				mediaFilter["type"] = enums.MediaTypeImage
			} else if file.Type == enums.FileTypeVideo {
				// Legacy flat video image requests resolve to the generated thumbnail.
				mediaFilter["type"] = enums.MediaTypeThumbnail
			}

			if requested.Asset == "short" {
				// A stream File may also contain HLS renditions; select the requested object.
				mediaFilter["key"] = bson.M{"$regex": "(?i)\\." + regexp.QuoteMeta(requested.Extension) + "$"}
			}

			err = models.MediaModel.Col().FindOne(
				ctx,
				mediaFilter,
				options.FindOne().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}),
			).Decode(&media)
			if err != nil {
				log.Printf("[Stream] Media not found for fileId=%s: %v", file.ID, err)
				sendNotFound(w, r, http.StatusNotFound)
				return
			}
		} else if !requested.Nested {
			// ─── Step 2b: Custom images are addressed by media.slug ─────
			err = models.MediaModel.Col().FindOne(ctx, bson.M{
				"slug": fileSlug,
				"type": enums.MediaTypeImage,
			}).Decode(&media)
			if err != nil {
				log.Printf("[Stream] File or image media not found for slug=%s: %v", fileSlug, err)
				sendNotFound(w, r, http.StatusNotFound)
				return
			}
			log.Printf("[Stream] Resolved direct image media slug=%s mediaId=%s", fileSlug, media.ID)
			err = models.FileModel.Col().FindOne(ctx, bson.M{"_id": media.FileID, "status": "ready"}).Decode(&file)
			if err != nil || file.IsTrashed() || file.IsDeleted() {
				sendNotFound(w, r, http.StatusNotFound)
				return
			}
		} else {
			sendNotFound(w, r, http.StatusNotFound)
			return
		}

		if requested.Nested && requested.Asset != "thumb" && !mediaMatchesExtension(media, requested.Extension) {
			sendNotFound(w, r, http.StatusNotFound)
			return
		}

		// ─── Step 3: Find storage by storageId ───────────────────────────
		storageID := strings.TrimSpace(media.StorageID)
		if storageID == "" {
			sendNotFound(w, r, http.StatusNotFound)
			return
		}

		storage, ok := getOnlineStorage(storageID)
		if !ok {
			log.Printf("[Stream] Storage not found in snapshot for storageId=%s", storageID)
			sendNotFound(w, r, http.StatusNotFound)
			return
		}

		publicURL := storage.GetPublicBaseURL()
		if publicURL == "" {
			log.Printf("[Stream] Storage %s has no publicUrl", storage.ID)
			sendNotFound(w, r, http.StatusInternalServerError)
			return
		}

		sourceURL, err = storageAssetURL(publicURL, file, media)
		if err != nil {
			log.Printf("[Stream] Cannot resolve media path for mediaId=%s: %v", media.ID, err)
			sendNotFound(w, r, http.StatusNotFound)
			return
		}
		mediaMime = media.Mime
		cache.SetJSON(lookupKey, &publicAssetLookup{SourceURL: sourceURL, Mime: mediaMime})
	}

	// ─── Step 4: Build source URL & proxy stream ─────────────────────────
	if sourceURL == "" {
		sendNotFound(w, r, http.StatusNotFound)
		return
	}
	log.Printf("[Stream] Proxying: slug=%s → %s", fileSlug, sourceURL)

	upstreamMethod := r.Method
	if requested.Asset == "thumb" {
		upstreamMethod = http.MethodGet
	}
	upstreamReq, err := http.NewRequestWithContext(ctx, upstreamMethod, sourceURL, nil)
	if err != nil {
		log.Printf("[Stream] Failed to create upstream request: %v", err)
		sendNotFound(w, r, http.StatusInternalServerError)
		return
	}

	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		upstreamReq.Header.Set("Range", rangeHeader)
	}

	client := &http.Client{Timeout: 0} // no timeout — streaming
	resp, err := client.Do(upstreamReq)
	if err != nil {
		log.Printf("[Stream] Upstream request failed: %v", err)
		sendNotFound(w, r, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		sendNotFound(w, r, http.StatusNotFound)
		return
	}

	// ─── Step 5: Check for image resize params ───────────────────────────
	imgParams := parseImageParams(r)
	if requested.Asset == "thumb" {
		imgParams = &ImageParams{Width: 330, Height: 168, Fit: "cover", Quality: 80, WebP: true, Thumbnail: true}
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = mediaMime
	}

	if imgParams != nil && isImageContentType(contentType) {
		imgData, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[Stream] Failed to read image body: %v", err)
			sendNotFound(w, r, http.StatusInternalServerError)
			return
		}

		resized, outType, err := resizeImage(imgData, contentType, imgParams)
		if err != nil {
			log.Printf("[Stream] Failed to resize image: %v", err)
			// Fallback: serve original
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Content-Length", strconv.Itoa(len(imgData)))
			w.Header().Set("Cache-Control", "public, max-age=63072000, immutable")
			w.WriteHeader(http.StatusOK)
			w.Write(imgData)
			return
		}

		w.Header().Set("Content-Type", outType)
		w.Header().Set("Content-Length", strconv.Itoa(len(resized)))
		w.Header().Set("Cache-Control", "public, max-age=63072000, immutable")
		w.WriteHeader(http.StatusOK)
		w.Write(resized)
		return
	}
	if r.Method == http.MethodHead {
		for _, header := range []string{"Content-Type", "Content-Length", "Accept-Ranges", "Content-Range", "ETag", "Last-Modified"} {
			if value := resp.Header.Get(header); value != "" {
				w.Header().Set(header, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		return
	}

	// ─── Step 6: Proxy response headers & body ───────────────────────────
	forwardHeaders := []string{
		"Content-Type", "Content-Length", "Content-Range",
		"Accept-Ranges", "Content-Disposition", "ETag", "Last-Modified",
	}
	for _, header := range forwardHeaders {
		if v := resp.Header.Get(header); v != "" {
			w.Header().Set(header, v)
		}
	}

	w.Header().Set("Cache-Control", "public, max-age=63072000, immutable")

	if w.Header().Get("Content-Type") == "" && mediaMime != "" {
		w.Header().Set("Content-Type", mediaMime)
	}
	if w.Header().Get("Accept-Ranges") == "" {
		w.Header().Set("Accept-Ranges", "bytes")
	}

	w.WriteHeader(resp.StatusCode)

	buf := make([]byte, 32*1024)
	io.CopyBuffer(w, resp.Body, buf)
}

func publicFileFilter(requested publicFileRequest) bson.M {
	filter := bson.M{"slug": requested.Slug, "status": "ready"}
	if requested.Nested {
		switch requested.Asset {
		case "preview":
			filter["kind"] = "preview"
		case "short":
			filter["kind"] = "stream"
		default:
			filter["kind"] = "poster"
		}
	}
	return filter
}

func parsePublicFileRequest(requestPath string) (publicFileRequest, bool) {
	cleaned := strings.Trim(strings.TrimSpace(requestPath), "/")
	parts := strings.Split(cleaned, "/")
	if len(parts) == 1 {
		dot := strings.LastIndex(parts[0], ".")
		if dot <= 0 || dot == len(parts[0])-1 {
			return publicFileRequest{}, false
		}
		return publicFileRequest{Slug: parts[0][:dot], Extension: strings.ToLower(parts[0][dot+1:])}, true
	}
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return publicFileRequest{}, false
	}
	dot := strings.LastIndex(parts[1], ".")
	if dot <= 0 || dot == len(parts[1])-1 {
		return publicFileRequest{}, false
	}
	asset := parts[1][:dot]
	extension := strings.ToLower(parts[1][dot+1:])
	if asset != "poster" && asset != "thumb" && asset != "preview" && asset != "short" {
		return publicFileRequest{}, false
	}
	if asset == "short" && extension != "mp4" && extension != "webm" && extension != "mov" && extension != "m4v" {
		return publicFileRequest{}, false
	}
	if asset == "thumb" && extension != "webp" {
		return publicFileRequest{}, false
	}
	return publicFileRequest{Slug: parts[0], Asset: asset, Extension: extension, Nested: true}, true
}

func mediaMatchesExtension(media models.Media, extension string) bool {
	name := strings.ToLower(media.EffectiveFileName())
	return extension != "" && strings.HasSuffix(name, "."+strings.ToLower(extension))
}

func storageAssetURL(publicURL string, file models.File, media models.Media) (string, error) {
	objectPath := strings.ReplaceAll(strings.TrimSpace(media.Key), "\\", "/")
	cleaned := path.Clean(objectPath)
	if objectPath == "" || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(objectPath, "/") {
		return "", fmt.Errorf("media key is unsafe")
	}
	prefix := strings.TrimSpace(file.ID) + "/"
	if file.Slug != "" && strings.HasPrefix(cleaned, prefix) {
		relativePath := strings.TrimPrefix(cleaned, prefix)
		if relativePath == "" {
			return "", fmt.Errorf("media key has no file path")
		}
		return url.JoinPath(publicURL, file.Slug, relativePath)
	}
	// Image/preview storages expose their object keys directly through publicUrl.
	return url.JoinPath(publicURL, cleaned)
}
