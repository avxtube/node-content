package handlers

import (
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"node-content/internal/cache"
	"node-content/internal/core/enums"
	"node-content/internal/db/models"
	"node-content/internal/utils"
)

// HandleSpriteVTT handles GET /{fileSlug}/sprite/sprite.vtt
func (h *Handler) HandleSpriteVTT(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	slug := strings.TrimSuffix(path, "/sprite/sprite.vtt")

	if slug == "" || strings.Contains(slug, "/") {
		HandleNotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// ─── Step 1: Find file by slug ───────────────────────────────────────
	var file models.File
	err := models.FileModel.Col().FindOne(ctx, bson.M{
		"slug":   slug,
		"kind":   "stream",
		"status": bson.M{"$in": playableFileStatuses()},
	}).Decode(&file)
	if err != nil {
		log.Printf("[Sprite] File not found: %s", slug)
		HandleNotFound(w, r)
		return
	}

	if file.IsTrashed() || file.IsDeleted() {
		HandleNotFound(w, r)
		return
	}

	// ─── Step 2: Find thumbnail media ────────────────────────────────────
	media, storedManifest, err := findSpriteMedia(ctx, file.ID)
	if err != nil {
		log.Printf("[Sprite] Thumbnail media not found for fileId=%s: %v", file.ID, err)
		HandleNotFound(w, r)
		return
	}

	var vttContent string
	if storedManifest {
		storage, ok := getOnlineStorage(strings.TrimSpace(media.StorageID))
		if !ok {
			log.Printf("[Sprite] Storage not found: %s", media.StorageID)
			HandleNotFound(w, r)
			return
		}
		vttURL, resolveErr := spriteSourceURL(&storage, &media, file.Slug, "sprite.vtt")
		if resolveErr == nil {
			vttContent, resolveErr = utils.FetchURLContent(ctx, vttURL)
		}
		err = resolveErr
	} else {
		// node-content owns and serves proxy-descriptor VTT files. node-proxy
		// remains responsible only for video segment bytes.
		vttContent, err = buildSpriteVTT(media)
	}
	if err != nil {
		log.Printf("[Sprite] Cannot build VTT for fileId=%s: %v", file.ID, err)
		HandleNotFound(w, r)
		return
	}

	responseBody := []byte(vttContent)
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(responseBody)))
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=63072000, immutable")
	w.Write(responseBody)
}

// HandleSpriteImage handles GET /{fileSlug}/sprite/{n}.jpg
func (h *Handler) HandleSpriteImage(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(path, "/sprite/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		HandleNotFound(w, r)
		return
	}

	slug := parts[0]
	filename := parts[1]

	if !isValidSpriteFilename(filename) {
		HandleNotFound(w, r)
		return
	}

	if strings.Contains(slug, "/") {
		HandleNotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	cacheKey := "sprite_delivery_v2:" + slug
	var delivery spriteDelivery
	if cache.GetJSON(cacheKey, &delivery) && delivery.URLPrefix != "" {
		w.Header().Set("X-Lookup-Cache", "HIT")
	} else {
		w.Header().Set("X-Lookup-Cache", "MISS")
		resolved, err := resolveSpriteDelivery(ctx, slug)
		if err != nil {
			log.Printf("[Sprite] Cannot resolve image delivery: %v", err)
			HandleNotFound(w, r)
			return
		}
		delivery = resolved
		cache.SetJSON(cacheKey, &delivery)
	}
	sourceURL, err := delivery.imageURL(filename)
	if err != nil {
		HandleNotFound(w, r)
		return
	}

	// Proxy image bytes directly without storing them in the local cache.
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		HandleNotFound(w, r)
		return
	}
	upstreamReq.Header.Set("Referer", "https://missav.ai/")
	upstreamReq.Header.Set("User-Agent", "ContentNode/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		log.Printf("[Sprite] Upstream request failed: %s → %v", sourceURL, err)
		HandleNotFound(w, r)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		HandleNotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	w.Header().Set("Cache-Control", "public, max-age=63072000, immutable")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)

	buf := make([]byte, 32*1024)
	io.CopyBuffer(w, resp.Body, buf)
}

type spriteDelivery struct {
	Delivery   string `json:"delivery"`
	URLPrefix  string `json:"urlPrefix"`
	URLSuffix  string `json:"urlSuffix"`
	FirstIndex int    `json:"firstIndex"`
	ImageCount int    `json:"imageCount"`
}

func (delivery spriteDelivery) imageURL(filename string) (string, error) {
	name := strings.TrimSuffix(strings.TrimPrefix(filename, "sprite-"), ".jpg")
	index, err := strconv.Atoi(name)
	if err != nil || index < 0 || index >= delivery.ImageCount || delivery.URLPrefix == "" {
		return "", fmt.Errorf("sprite index out of range")
	}
	return delivery.URLPrefix + strconv.Itoa(index+delivery.FirstIndex) + delivery.URLSuffix, nil
}

func resolveSpriteDelivery(ctx context.Context, slug string) (spriteDelivery, error) {
	var file models.File
	err := models.FileModel.Col().FindOne(ctx, bson.M{
		"slug":   slug,
		"kind":   "stream",
		"status": bson.M{"$in": playableFileStatuses()},
	}).Decode(&file)
	if err != nil {
		return spriteDelivery{}, fmt.Errorf("file lookup: %w", err)
	}

	if file.IsTrashed() || file.IsDeleted() {
		return spriteDelivery{}, fmt.Errorf("file is deleted")
	}

	// ─── Step 2: Find thumbnail media ────────────────────────────────────
	media, _, err := findSpriteMedia(ctx, file.ID)
	if err != nil {
		return spriteDelivery{}, fmt.Errorf("thumbnail media lookup: %w", err)
	}

	// ─── Step 3: Find storage ────────────────────────────────────────────
	storageID := strings.TrimSpace(media.StorageID)

	storage, ok := getOnlineStorage(storageID)
	if !ok {
		return spriteDelivery{}, fmt.Errorf("storage not found: %s", storageID)
	}

	if storage.IsProxy() {
		delivery, err := proxySpriteDelivery(&media)
		if err != nil {
			return spriteDelivery{}, fmt.Errorf("resolve proxy storage %s: %w", storage.ID, err)
		}
		return delivery, nil
	}
	descriptor, err := readSpriteDescriptor(media)
	if err != nil {
		return spriteDelivery{}, err
	}
	prefix, err := storedSpritePrefix(&storage, &media, file.Slug)
	if err != nil {
		return spriteDelivery{}, fmt.Errorf("resolve storage %s: %w", storage.ID, err)
	}
	return spriteDelivery{
		Delivery: storage.Provider, URLPrefix: prefix, URLSuffix: ".jpg",
		ImageCount: descriptor.ImageCount,
	}, nil
}

func storedSpritePrefix(storage *models.Storage, media *models.Media, fileSlug string) (string, error) {
	if originURL := storage.GetOriginBaseURL(); originURL != "" {
		directory := path.Dir(media.ObjectPath())
		if directory == "." || directory == "/" {
			return "", fmt.Errorf("sprite media key has no directory")
		}
		return url.JoinPath(originURL, directory, "sprite-")
	}
	baseURL := storage.GetStorageBaseURL()
	if baseURL == "" || fileSlug == "" {
		return "", fmt.Errorf("storage URL or file slug is empty")
	}
	return url.JoinPath(baseURL, fileSlug, "sprite", "sprite-")
}

func findSpriteMedia(ctx context.Context, fileID string) (models.Media, bool, error) {
	base := bson.M{
		"fileId":    fileID,
		"type":      enums.MediaTypeThumbnail,
		"enabled":   bson.M{"$ne": false},
		"deletedAt": nil,
	}
	var stored models.Media
	storedFilter := bson.M{}
	for key, value := range base {
		storedFilter[key] = value
	}
	storedFilter["key"] = bson.M{"$regex": `(^|/)sprite\.vtt$`}
	if err := models.MediaModel.Col().FindOne(ctx, storedFilter).Decode(&stored); err == nil {
		return stored, true, nil
	}

	var descriptor models.Media
	descriptorFilter := bson.M{}
	for key, value := range base {
		descriptorFilter[key] = value
	}
	descriptorFilter["metadata.sprite"] = bson.M{"$type": "object"}
	if err := models.MediaModel.Col().FindOne(ctx, descriptorFilter).Decode(&descriptor); err != nil {
		return models.Media{}, false, err
	}
	return descriptor, false, nil
}

func spriteSourceURL(storage *models.Storage, media *models.Media, fileSlug, filename string) (string, error) {
	if storage.IsProxy() {
		return proxySpriteOriginURL(media, filename)
	}
	if originURL := storage.GetOriginBaseURL(); originURL != "" {
		objectPath := media.ObjectPath()
		if objectPath == "" {
			return "", fmt.Errorf("sprite media key is empty")
		}
		directory := path.Dir(objectPath)
		if directory == "." || directory == "/" {
			return "", fmt.Errorf("sprite media key has no directory")
		}
		return url.JoinPath(originURL, directory, filename)
	}

	baseURL := storage.GetStorageBaseURL()
	if baseURL == "" {
		return "", fmt.Errorf("storage base URL is empty")
	}
	if fileSlug == "" {
		return "", fmt.Errorf("sprite asset ID is empty")
	}
	return url.JoinPath(baseURL, fileSlug, "sprite", filename)
}

type spriteDescriptor struct {
	Columns         int
	Rows            int
	Width           int
	Height          int
	ImageCount      int
	FrameCount      int
	SecondsPerImage float64
}

func readSpriteDescriptor(media models.Media) (spriteDescriptor, error) {
	sprite := metadataObject(media.Metadata["sprite"])
	integer := func(name string) (int, bool) {
		value := metadataNumber(sprite, name)
		return int(value), value > 0 && value == math.Trunc(value)
	}
	columns, columnsOK := integer("col")
	rows, rowsOK := integer("row")
	width, widthOK := integer("width")
	height, heightOK := integer("height")
	frameCount, framesOK := integer("pic_num")
	imageCount, imagesOK := integer("imageCount")
	seconds := metadataNumber(sprite, "secondsPerImage")
	if !columnsOK || !rowsOK || !widthOK || !heightOK || !framesOK || seconds <= 0 {
		return spriteDescriptor{}, fmt.Errorf("incomplete sprite metadata")
	}
	if columns > 100 || rows > 100 || width > 4096 || height > 4096 || frameCount > 200000 {
		return spriteDescriptor{}, fmt.Errorf("sprite metadata exceeds limits")
	}
	cellsPerImage := columns * rows
	if !imagesOK {
		imageCount = (frameCount + cellsPerImage - 1) / cellsPerImage
	}
	if imageCount > 100000 {
		return spriteDescriptor{}, fmt.Errorf("sprite image count exceeds limits")
	}
	frameCount = min(frameCount, imageCount*cellsPerImage)
	return spriteDescriptor{columns, rows, width, height, imageCount, frameCount, seconds}, nil
}

func buildSpriteVTT(media models.Media) (string, error) {
	descriptor, err := readSpriteDescriptor(media)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	output.WriteString("WEBVTT\n\n")
	cellsPerImage := descriptor.Columns * descriptor.Rows
	for frame := 0; frame < descriptor.FrameCount; frame++ {
		sheet := frame / cellsPerImage
		cell := frame % cellsPerImage
		x := (cell % descriptor.Columns) * descriptor.Width
		y := (cell / descriptor.Columns) * descriptor.Height
		start := float64(frame) * descriptor.SecondsPerImage
		end := float64(frame+1) * descriptor.SecondsPerImage
		fmt.Fprintf(&output, "%s --> %s\nsprite-%d.jpg#xywh=%d,%d,%d,%d\n\n", spriteVTTTime(start), spriteVTTTime(end), sheet, x, y, descriptor.Width, descriptor.Height)
	}
	return output.String(), nil
}

func spriteVTTTime(seconds float64) string {
	milliseconds := int64(math.Round(seconds * 1000))
	hours := milliseconds / 3_600_000
	milliseconds %= 3_600_000
	minutes := milliseconds / 60_000
	milliseconds %= 60_000
	wholeSeconds := milliseconds / 1000
	milliseconds %= 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, wholeSeconds, milliseconds)
}

var markdownSpriteURL = regexp.MustCompile(`^\[(https?://[^\]]+)\]\(https?://[^)]+\)$`)

func proxySpriteOriginURL(media *models.Media, filename string) (string, error) {
	delivery, err := proxySpriteDelivery(media)
	if err != nil {
		return "", err
	}
	return delivery.imageURL(filename)
}

func proxySpriteDelivery(media *models.Media) (spriteDelivery, error) {
	descriptor, err := readSpriteDescriptor(*media)
	if err != nil {
		return spriteDelivery{}, err
	}
	sprite := metadataObject(media.Metadata["sprite"])
	template := firstString(sprite, "segmentTemplate", "sourceTemplate", "urlTemplate")
	if match := markdownSpriteURL.FindStringSubmatch(template); len(match) == 2 {
		template = match[1]
	}
	template = strings.ReplaceAll(strings.ReplaceAll(template, `\/`, "/"), `\_`, "_")
	count := strings.Count(template, "{index}") + strings.Count(template, "{{num}}")
	if count != 1 {
		return spriteDelivery{}, fmt.Errorf("sprite source template is invalid")
	}
	template = strings.ReplaceAll(template, "{{num}}", "{index}")
	parts := strings.Split(template, "{index}")
	parsed, err := url.Parse(parts[0] + "0" + parts[1])
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || !allowedSpriteHost(parsed.Hostname()) {
		return spriteDelivery{}, fmt.Errorf("sprite source URL is not allowed")
	}
	return spriteDelivery{
		Delivery: "proxy", URLPrefix: parts[0], URLSuffix: parts[1],
		FirstIndex: int(metadataNumber(sprite, "firstIndex")), ImageCount: descriptor.ImageCount,
	}, nil
}

func firstString(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func allowedSpriteHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, allowed := range []string{"surrit.com", "fourhoi.com"} {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

func isValidSpriteFilename(filename string) bool {
	// server-spritesheet สร้างไฟล์ชื่อ sprite-{n}.jpg (อ้างใน sprite.vtt)
	if !strings.HasPrefix(filename, "sprite-") || !strings.HasSuffix(filename, ".jpg") {
		return false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(filename, "sprite-"), ".jpg")
	if name == "" {
		return false
	}
	for _, c := range name {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
