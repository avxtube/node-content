package handlers

import (
	"testing"

	"node-content/internal/db/models"
)

func TestSpriteSourceURLUsesStorageNodeAndFileSlug(t *testing.T) {
	storage := &models.Storage{Provider: "s3", PublicURL: "https://stream.example.com"}
	media := &models.Media{FileID: "file-id"}

	got, err := spriteSourceURL(storage, media, "public-slug", "sprite-1.jpg")
	if err != nil {
		t.Fatalf("spriteSourceURL() error = %v", err)
	}
	want := "https://stream.example.com/public-slug/sprite/sprite-1.jpg"
	if got != want {
		t.Fatalf("spriteSourceURL() = %q, want %q", got, want)
	}
}
