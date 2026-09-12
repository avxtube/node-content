package handlers

import (
	"testing"

	"node-content/internal/db/models"
)

func TestSubtitleSourceURLUsesStorageNodeAndFileSlug(t *testing.T) {
	media := models.Media{FileID: "file-id", Key: "file-id/subtitle_1.vtt"}
	storage := models.Storage{Provider: "s3", PublicURL: "https://stream.example.com"}
	file := models.File{ID: "file-id", Slug: "video-slug"}

	got, err := subtitleSourceURL(media, storage, file)
	if err != nil {
		t.Fatalf("subtitleSourceURL: %v", err)
	}
	if want := "https://stream.example.com/video-slug/subtitle_1.vtt"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
