package handlers

import (
	"strings"
	"testing"

	"node-content/internal/db/models"
)

func TestRenderProxyMediaPlaylistIsOwnedByNodeContent(t *testing.T) {
	media := models.Media{
		Slug: "media-slug",
		Metadata: map[string]interface{}{
			"media": map[string]interface{}{
				"version":         3,
				"targetDuration":  4,
				"mediaSequence":   0,
				"playlistType":    "VOD",
				"endList":         true,
				"segmentCount":    2,
				"segmentTemplate": "https://surrit.com/movie/video{sequence}.jpeg",
			},
			"runs": []map[string]interface{}{{"from": 0, "to": 1, "duration": 4}},
		},
	}

	playlist, err := renderProxyMediaPlaylist(media, "https://proxy.content.test")
	if err != nil {
		t.Fatalf("renderProxyMediaPlaylist() error = %v", err)
	}
	for _, expected := range []string{
		"#EXT-X-TARGETDURATION:4",
		"https://proxy.content.test/media-slug/v-0.jpeg",
		"https://proxy.content.test/media-slug/v-1.jpeg",
		"#EXT-X-ENDLIST",
	} {
		if !strings.Contains(playlist, expected) {
			t.Fatalf("playlist does not contain %q:\n%s", expected, playlist)
		}
	}
	if strings.Contains(playlist, "/video.m3u8") || strings.Contains(playlist, "surrit.com") {
		t.Fatalf("node-content playlist leaked another playlist or origin URL:\n%s", playlist)
	}
}

func TestRenderProxyMediaPlaylistSupportsLegacyHLSMetadata(t *testing.T) {
	media := models.Media{
		Slug: "legacy-slug",
		Metadata: map[string]interface{}{
			"hls": map[string]interface{}{
				"media": map[string]interface{}{
					"segmentCount":    1,
					"segmentTemplate": "https://surrit.com/movie/video{sequence}.jpeg",
					"runs":            []map[string]interface{}{{"from": 0, "to": 0, "duration": 4}},
					"endList":         true,
				},
			},
		},
	}
	playlist, err := renderProxyMediaPlaylist(media, "https://proxy.content.test")
	if err != nil || !strings.Contains(playlist, "https://proxy.content.test/legacy-slug/v-0.jpeg") {
		t.Fatalf("legacy proxy playlist failed: err=%v playlist=%s", err, playlist)
	}
}
