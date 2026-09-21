package handlers

import (
	"strings"
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

func TestSpriteSourceURLUsesOriginDescriptorInsteadOfProxy(t *testing.T) {
	storage := &models.Storage{
		Provider:  "proxy",
		PublicURL: "https://proxy.example.com/base",
		OriginURL: "https://origin.example.com/should-not-be-used",
	}
	media := &models.Media{FileID: "file-id", Key: "proxy/file-id/sprite/{index}.jpg", Metadata: map[string]interface{}{
		"sprite": map[string]interface{}{
			"col": 2, "row": 2, "width": 300, "height": 168,
			"pic_num": 8, "imageCount": 2, "secondsPerImage": 2,
			"firstIndex": 10, "segmentTemplate": `https:\/\/surrit.com\/movie\/seek\/_{{num}}.jpg`,
		},
	}}

	got, err := spriteSourceURL(storage, media, "content-slug", "sprite-1.jpg")
	if err != nil {
		t.Fatalf("spriteSourceURL() error = %v", err)
	}
	if want := "https://surrit.com/movie/seek/_11.jpg"; got != want {
		t.Fatalf("spriteSourceURL() = %q, want %q", got, want)
	}
}

func TestProxySpriteDeliveryCachesOnePrefixForEveryImage(t *testing.T) {
	media := &models.Media{Metadata: map[string]interface{}{
		"sprite": map[string]interface{}{
			"col": 2, "row": 2, "width": 300, "height": 168,
			"pic_num": 8, "imageCount": 2, "secondsPerImage": 2,
			"firstIndex": 10, "segmentTemplate": `https:\/\/surrit.com\/movie\/seek\/_{{num}}.jpg`,
		},
	}}

	delivery, err := proxySpriteDelivery(media)
	if err != nil {
		t.Fatalf("proxySpriteDelivery() error = %v", err)
	}
	if delivery.Delivery != "proxy" || delivery.URLPrefix != "https://surrit.com/movie/seek/_" || delivery.URLSuffix != ".jpg" {
		t.Fatalf("unexpected delivery: %#v", delivery)
	}
	got, err := delivery.imageURL("sprite-1.jpg")
	if err != nil {
		t.Fatalf("imageURL() error = %v", err)
	}
	if want := "https://surrit.com/movie/seek/_11.jpg"; got != want {
		t.Fatalf("imageURL() = %q, want %q", got, want)
	}
}

func TestBuildSpriteVTTInNodeContent(t *testing.T) {
	media := models.Media{Metadata: map[string]interface{}{
		"sprite": map[string]interface{}{
			"col": 2, "row": 2, "width": 300, "height": 168,
			"pic_num": 5, "imageCount": 2, "secondsPerImage": 2,
		},
	}}
	vtt, err := buildSpriteVTT(media)
	if err != nil {
		t.Fatalf("buildSpriteVTT() error = %v", err)
	}
	if want := "00:00:08.000 --> 00:00:10.000\nsprite-1.jpg#xywh=0,0,300,168"; !strings.Contains(vtt, want) {
		t.Fatalf("VTT does not contain %q:\n%s", want, vtt)
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
