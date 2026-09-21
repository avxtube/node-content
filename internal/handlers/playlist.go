package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"node-content/internal/cache"
	"node-content/internal/core/enums"
	"node-content/internal/db/models"
	"node-content/internal/utils"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type masterPlaylistMetadata struct {
	File      models.File    `json:"file"`
	Playlists []models.Media `json:"playlists"`
	Audio     []models.Media `json:"audio,omitempty"`
}

// HandlePlaylist handles GET /{fileSlug}/playlist.m3u8
// Flow: fileSlug → file._id → medias (video type) → master HLS playlist
func (h *Handler) HandlePlaylist(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	slug := strings.TrimSuffix(path, "/playlist.m3u8")

	if slug == "" {
		HandleNotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var file models.File
	var medias []models.Media
	var audioMedias []models.Media
	cacheKey := "playlist_master_metadata_v3:" + slug
	var metadata masterPlaylistMetadata
	if cache.GetJSON(cacheKey, &metadata) {
		file = metadata.File
		medias = metadata.Playlists
		audioMedias = metadata.Audio
		w.Header().Set("X-Lookup-Cache", "HIT")
	} else {
		w.Header().Set("X-Lookup-Cache", "MISS")

		// ─── Step 1: Find file by slug ───────────────────────────────────
		err := models.FileModel.Col().FindOne(ctx, bson.M{
			"slug": slug, "kind": "stream", "status": bson.M{"$in": playableFileStatuses()},
			"metadata.deletedAt": nil, "metadata.trashedAt": nil,
		}).Decode(&file)
		if err != nil {
			log.Printf("[Playlist] File not found for slug=%s: %v", slug, err)
			if errors.Is(err, mongo.ErrNoDocuments) {
				HandleNotFound(w, r)
			} else {
				HandleCachedError(w, r, http.StatusInternalServerError)
			}
			return
		}

		// ─── Step 2: Find all video media for this file ──────────────────
		mediaFilter := bson.M{
			"fileId":    file.ID,
			"type":      enums.MediaTypeVideo,
			"enabled":   bson.M{"$ne": false},
			"deletedAt": nil,
			"quality": bson.M{"$in": []string{
				enums.ResolutionOriginal,
				enums.Resolution1080,
				enums.Resolution720,
				enums.Resolution480,
				enums.Resolution360,
			}},
		}

		cursor, err := models.MediaModel.Col().Find(ctx, mediaFilter)
		if err != nil {
			log.Printf("[Playlist] Error finding media for fileId=%s: %v", file.ID, err)
			HandleCachedError(w, r, http.StatusInternalServerError)
			return
		}
		defer cursor.Close(ctx)

		for cursor.Next(ctx) {
			var media models.Media
			if err := cursor.Decode(&media); err != nil {
				continue
			}
			medias = append(medias, media)
		}
		if err := cursor.Err(); err != nil {
			log.Printf("[Playlist] Error reading media for fileId=%s: %v", file.ID, err)
			HandleCachedError(w, r, http.StatusInternalServerError)
			return
		}

		if len(medias) == 0 {
			log.Printf("[Playlist] No video media found for fileId=%s", file.ID)
			HandleNotFound(w, r)
			return
		}

		// Media records are the source of truth. Legacy clones may already have
		// separated audio while File.metadata.mediaLayout is still missing.
		audioCursor, audioErr := models.MediaModel.Col().Find(ctx, bson.M{
			"fileId": file.ID,
			"type":   enums.MediaTypeAudio,
		})
		if audioErr != nil {
			log.Printf("[Playlist] Error finding audio media for fileId=%s: %v", file.ID, audioErr)
			HandleCachedError(w, r, http.StatusInternalServerError)
			return
		}
		defer audioCursor.Close(ctx)
		for audioCursor.Next(ctx) {
			var media models.Media
			if err := audioCursor.Decode(&media); err == nil {
				audioMedias = append(audioMedias, media)
			}
		}
		if err := audioCursor.Err(); err != nil {
			log.Printf("[Playlist] Error reading audio media for fileId=%s: %v", file.ID, err)
			HandleCachedError(w, r, http.StatusInternalServerError)
			return
		}
		if file.Metadata != nil && file.Metadata.MediaLayout != nil && *file.Metadata.MediaLayout == "separated" {
			if file.Metadata.AudioTrackCount != nil && *file.Metadata.AudioTrackCount > 0 && len(audioMedias) == 0 {
				log.Printf("[Playlist] Separated file is missing %d expected audio track(s): fileId=%s", *file.Metadata.AudioTrackCount, file.ID)
				HandleCachedError(w, r, http.StatusServiceUnavailable)
				return
			}
		}
		cache.SetJSON(cacheKey, &masterPlaylistMetadata{File: file, Playlists: medias, Audio: audioMedias})
	}
	sort.SliceStable(audioMedias, func(i, j int) bool {
		if !audioMedias[i].CreatedAt.Equal(audioMedias[j].CreatedAt) {
			return audioMedias[i].CreatedAt.Before(audioMedias[j].CreatedAt)
		}
		return audioMedias[i].ID < audioMedias[j].ID
	})

	// If standard resolutions exist (1080/720/480/360), hide "original"
	hasStandard := false
	for _, m := range medias {
		res := ""
		if m.Quality != nil {
			res = *m.Quality
		}
		if res == enums.Resolution1080 || res == enums.Resolution720 ||
			res == enums.Resolution480 || res == enums.Resolution360 {
			hasStandard = true
			break
		}
	}
	if hasStandard {
		filtered := make([]models.Media, 0, len(medias))
		for _, m := range medias {
			if m.Quality == nil || *m.Quality != enums.ResolutionOriginal {
				filtered = append(filtered, m)
			}
		}
		medias = filtered
	}

	// ─── Step 3: Generate master playlist ────────────────────────────────
	host := r.Host
	var playlist strings.Builder

	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString("#EXT-X-VERSION:6\n")
	writeAudioRenditions(&playlist, host, audioMedias)

	storageCache := make(map[string]models.Storage)

	for _, media := range medias {
		var streamInf string

		storageID := strings.TrimSpace(media.StorageID)

		if storageID != "" {
			storage, ok := storageCache[storageID]
			if !ok {
				storage, ok = getOnlineStorage(storageID)
				if ok {
					storageCache[storageID] = storage
				} else {
					log.Printf("[Playlist] Storage not found: %s", storageID)
				}
			}

			playbackBaseURL := storage.GetPlaybackBaseURL()
			if storage.IsProxy() {
				streamInf = streamInfoFromMedia(media)
			} else if playbackBaseURL != "" {
				masterURL := fmt.Sprintf("%s/%s/master.m3u8", playbackBaseURL, media.Slug)
				content, err := utils.FetchURLContent(ctx, masterURL)
				if err == nil {
					streamInf = extractStreamInf(content)
				} else {
					log.Printf("[Playlist] Failed to fetch master from %s: %v", masterURL, err)
				}
			}
		}

		// Fallback to estimation if fetch failed
		if streamInf == "" {
			resolution := ""
			if media.Quality != nil {
				resolution = *media.Quality
			}
			bandwidth := getEstimatedBandwidth(resolution)
			width, height := getResolutionDimensions(resolution)

			streamInf = "#EXT-X-STREAM-INF:BANDWIDTH=" + bandwidth
			if width > 0 && height > 0 {
				streamInf += ",RESOLUTION=" + formatResolution(width, height)
			}
		}

		if len(audioMedias) > 0 {
			streamInf = attachAudioGroup(streamInf)
		}
		playlist.WriteString(streamInf + "\n")
		playlist.WriteString("//" + host + "/" + media.Slug + "/video.m3u8\n")
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("CDN-Cache-Control", "public, max-age=300")

	w.Write([]byte(playlist.String()))
}

func streamInfoFromMedia(media models.Media) string {
	resolution := ""
	if media.Quality != nil {
		resolution = *media.Quality
	}
	bandwidth := metadataNumber(media.Metadata, "bandwidth")
	if hls := metadataObject(media.Metadata["hls"]); len(hls) > 0 {
		if value := metadataNumber(hls, "bandwidth"); value > 0 {
			bandwidth = value
		}
	}
	if rawAttributes := metadataObject(media.Metadata["rawAttributes"]); bandwidth <= 0 {
		bandwidth = metadataNumber(rawAttributes, "BANDWIDTH")
	}
	if bandwidth <= 0 {
		fmt.Sscanf(getEstimatedBandwidth(resolution), "%f", &bandwidth)
	}
	width, height := getResolutionDimensions(resolution)
	if media.Width != nil && *media.Width > 0 {
		width = *media.Width
	}
	if media.Height != nil && *media.Height > 0 {
		height = *media.Height
	}
	line := fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%.0f", bandwidth)
	if width > 0 && height > 0 {
		line += ",RESOLUTION=" + formatResolution(width, height)
	}
	return line
}

func metadataNumber(values map[string]interface{}, key string) float64 {
	if values == nil {
		return 0
	}
	switch value := values[key].(type) {
	case int:
		return float64(value)
	case int32:
		return float64(value)
	case int64:
		return float64(value)
	case float32:
		return float64(value)
	case float64:
		return value
	case string:
		var number float64
		fmt.Sscanf(strings.TrimSpace(value), "%f", &number)
		return number
	default:
		return 0
	}
}

func metadataObject(value interface{}) map[string]interface{} {
	switch object := value.(type) {
	case map[string]interface{}:
		return object
	case bson.M:
		return map[string]interface{}(object)
	default:
		return nil
	}
}

func writeAudioRenditions(playlist *strings.Builder, host string, medias []models.Media) {
	if len(medias) == 0 {
		return
	}
	usedNames := make(map[string]int)
	for index, media := range medias {
		language := "und"
		name := ""
		isDefault := index == 0
		if name == "" {
			name = language
			if name == "und" {
				name = fmt.Sprintf("Audio %d", index+1)
			}
		}
		usedNames[name]++
		if usedNames[name] > 1 {
			name = fmt.Sprintf("%s %d", name, usedNames[name])
		}
		defaultValue := "NO"
		if isDefault {
			defaultValue = "YES"
		}
		fmt.Fprintf(playlist,
			"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"%s\",LANGUAGE=\"%s\",DEFAULT=%s,AUTOSELECT=YES,URI=\"//%s/%s/audio.m3u8\"\n",
			hlsAttribute(name), hlsAttribute(language), defaultValue, host, media.Slug)
	}
}

func hlsAttribute(value string) string {
	value = strings.ReplaceAll(value, "\\", "")
	return strings.ReplaceAll(value, "\"", "'")
}

func attachAudioGroup(streamInf string) string {
	if strings.Contains(streamInf, `AUDIO="audio"`) {
		return streamInf
	}
	if marker := `CODECS="`; strings.Contains(streamInf, marker) {
		start := strings.Index(streamInf, marker) + len(marker)
		if endOffset := strings.Index(streamInf[start:], `"`); endOffset >= 0 {
			end := start + endOffset
			if !strings.Contains(streamInf[start:end], "mp4a") {
				streamInf = streamInf[:end] + ",mp4a.40.2" + streamInf[end:]
			}
		}
	} else {
		streamInf += `,CODECS="avc1.64001f,mp4a.40.2"`
	}
	return streamInf + `,AUDIO="audio"`
}

func extractStreamInf(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#EXT-X-STREAM-INF") {
			return trimmed
		}
	}
	return ""
}

func getEstimatedBandwidth(resolution string) string {
	switch resolution {
	case "original":
		return "8000000"
	case "2160", "4k", "4K":
		return "15000000"
	case "1080", "1080p":
		return "5000000"
	case "720", "720p":
		return "2500000"
	case "480", "480p":
		return "1000000"
	case "360", "360p":
		return "500000"
	default:
		return "2500000"
	}
}

func getResolutionDimensions(resolution string) (int, int) {
	switch resolution {
	case "original":
		return 0, 0
	case "2160", "4k", "4K":
		return 3840, 2160
	case "1080", "1080p":
		return 1920, 1080
	case "720", "720p":
		return 1280, 720
	case "480", "480p":
		return 854, 480
	case "360", "360p":
		return 640, 360
	default:
		return 1280, 720
	}
}

func formatResolution(width, height int) string {
	return fmt.Sprintf("%dx%d", width, height)
}
