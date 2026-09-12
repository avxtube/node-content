package handlers

import (
	"testing"

	"node-content/internal/db/models"
)

func TestStorageAssetURLUsesPublicFileSlugAndRelativeMediaKey(t *testing.T) {
	file := models.File{ID: "file-id", Slug: "public-slug"}
	media := models.Media{FileID: file.ID, Key: "file-id/subtitles/en.vtt"}

	got, err := storageAssetURL("https://storage.example/base", file, media)
	if err != nil {
		t.Fatalf("storageAssetURL returned error: %v", err)
	}
	if want := "https://storage.example/base/public-slug/subtitles/en.vtt"; got != want {
		t.Fatalf("storageAssetURL = %q, want %q", got, want)
	}
}

func TestStorageAssetURLUsesDirectKeyForImageAndPreviewStorage(t *testing.T) {
	file := models.File{ID: "file-id", Slug: "public-slug"}
	media := models.Media{FileID: file.ID, Key: "2026-09-11/image.webp"}

	got, err := storageAssetURL("https://media.example", file, media)
	if err != nil {
		t.Fatalf("storageAssetURL returned error: %v", err)
	}
	if want := "https://media.example/2026-09-11/image.webp"; got != want {
		t.Fatalf("storageAssetURL = %q, want %q", got, want)
	}
}

func TestStorageAssetURLRejectsUnsafeKey(t *testing.T) {
	file := models.File{ID: "file-id", Slug: "public-slug"}
	for _, key := range []string{"../secret", "/absolute/file.jpg", ""} {
		if _, err := storageAssetURL("https://storage.example", file, models.Media{Key: key}); err == nil {
			t.Fatalf("storageAssetURL should reject %q", key)
		}
	}
}

func TestParsePublicFileRequest(t *testing.T) {
	tests := []struct {
		path      string
		slug      string
		asset     string
		extension string
	}{
		{"/poster-slug/poster.jpg", "poster-slug", "poster", "jpg"},
		{"/poster-slug/thumb.webp", "poster-slug", "thumb", "webp"},
		{"/preview-slug/preview.mp4", "preview-slug", "preview", "mp4"},
	}
	for _, test := range tests {
		got, ok := parsePublicFileRequest(test.path)
		if !ok {
			t.Fatalf("parsePublicFileRequest(%q) was rejected", test.path)
		}
		if got.Slug != test.slug || got.Asset != test.asset || got.Extension != test.extension || !got.Nested {
			t.Fatalf("parsePublicFileRequest(%q) = %#v", test.path, got)
		}
	}
}

func TestParsePublicFileRequestRejectsInvalidAsset(t *testing.T) {
	for _, path := range []string{"/slug/thumb.jpg", "/slug/other.mp4", "/slug/preview", "/a/b/c.jpg"} {
		if _, ok := parsePublicFileRequest(path); ok {
			t.Fatalf("parsePublicFileRequest(%q) should reject the path", path)
		}
	}
}
