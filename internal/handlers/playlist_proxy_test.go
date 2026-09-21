package handlers

import (
	"testing"

	"node-content/internal/db/models"
)

func TestStreamInfoFromProxyMediaUsesStoredDescriptor(t *testing.T) {
	quality := "480"
	width, height := 842, 480
	media := models.Media{
		Quality: &quality,
		Width:   &width,
		Height:  &height,
		Metadata: map[string]interface{}{
			"rawAttributes": map[string]interface{}{"BANDWIDTH": "1400000"},
		},
	}

	got := streamInfoFromMedia(media)
	want := "#EXT-X-STREAM-INF:BANDWIDTH=1400000,RESOLUTION=842x480"
	if got != want {
		t.Fatalf("streamInfoFromMedia() = %q, want %q", got, want)
	}
}
