package handlers

import (
	"testing"

	"node-content/internal/db/models"
)

func TestSpriteSourceURLUsesPublicStorageAndFileSlugWithoutOrigin(t *testing.T) {
	storage := &models.Storage{Provider: "s3", PublicURL: "https://stream.example.com"}
	media := &models.Media{FileID: "file-id", Key: "file-id/sprite/sprite.vtt"}

	got, err := spriteSourceURL(storage, media, "public-slug", "sprite-1.jpg")
	if err != nil {
		t.Fatalf("spriteSourceURL() error = %v", err)
	}
	want := "https://stream.example.com/public-slug/sprite/sprite-1.jpg"
	if got != want {
		t.Fatalf("spriteSourceURL() = %q, want %q", got, want)
	}
}

func TestSpriteSourceURLUsesOriginAndMediaKey(t *testing.T) {
	storage := &models.Storage{
		Provider:  "s3",
		PublicURL: "https://stream.example.com",
		OriginURL: "https://origin.example.com",
	}
	media := &models.Media{FileID: "file-id", Key: "file-id/sprite/sprite.vtt"}

	got, err := spriteSourceURL(storage, media, "public-slug", "sprite-2.jpg")
	if err != nil {
		t.Fatalf("spriteSourceURL() error = %v", err)
	}
	want := "https://origin.example.com/file-id/sprite/sprite-2.jpg"
	if got != want {
		t.Fatalf("spriteSourceURL() = %q, want %q", got, want)
	}
}
