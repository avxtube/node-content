package handlers

import (
	"testing"

	"node-content/internal/db/models"
)

func TestSelectPosterMediaUsesLowestNumericResolution(t *testing.T) {
	resolutionOriginal := "original"
	resolution1080 := "1080"
	resolution360 := "360"
	medias := []models.Media{
		{Slug: "original", Quality: &resolutionOriginal},
		{Slug: "1080", Quality: &resolution1080},
		{Slug: "360", Quality: &resolution360},
	}

	media, ok := selectPosterMedia(medias)
	if !ok || media.Slug != "360" {
		t.Fatalf("expected lowest resolution 360, got %#v (ok=%v)", media, ok)
	}
}

func TestSelectPosterMediaFallsBackToOriginal(t *testing.T) {
	resolutionOriginal := "original"
	media, ok := selectPosterMedia([]models.Media{{
		Slug:    "original",
		Quality: &resolutionOriginal,
	}})
	if !ok || media.Slug != "original" {
		t.Fatalf("expected original fallback, got %#v (ok=%v)", media, ok)
	}
}

func TestResolvePosterSecondUsesFileDurationAndClamps(t *testing.T) {
	fileDuration := 100.0
	file := models.File{Metadata: &models.FileMetadata{Duration: &fileDuration}}
	media := models.Media{}

	if got := resolvePosterSecond("poster", true, file, media); got != 50 {
		t.Fatalf("expected file midpoint 50, got %d", got)
	}
	if got := resolvePosterSecond("150", false, file, media); got != 99 {
		t.Fatalf("expected clamped second 99, got %d", got)
	}
}

func TestPosterDeliveryBuildsDynamicThumbnailURL(t *testing.T) {
	delivery := posterDelivery{
		Delivery:       "s3",
		URLPrefix:      "https://storage.example.com/media-id/thumb-",
		URLSuffix:      "-w500.jpg",
		DurationSecond: 100,
	}

	got, err := delivery.imageURL("poster", true)
	if err != nil {
		t.Fatalf("imageURL() error = %v", err)
	}
	if want := "https://storage.example.com/media-id/thumb-50000-w500.jpg"; got != want {
		t.Fatalf("imageURL() = %q, want %q", got, want)
	}
}
