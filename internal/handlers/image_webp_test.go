package handlers

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func TestResizeImageCreatesWebPThumbnail(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 660, 336))
	for y := 0; y < 336; y++ {
		for x := 0; x < 660; x++ {
			source.Set(x, y, color.NRGBA{R: 200, G: 80, B: 40, A: 255})
		}
	}
	var input bytes.Buffer
	if err := jpeg.Encode(&input, source, nil); err != nil {
		t.Fatal(err)
	}

	output, mime, err := resizeImage(input.Bytes(), "image/jpeg", &ImageParams{
		Width: 330, Height: 168, Fit: "cover", Quality: 80, WebP: true, Thumbnail: true,
	})
	if err != nil {
		t.Fatalf("resizeImage returned error: %v", err)
	}
	if mime != "image/webp" {
		t.Fatalf("resizeImage mime = %q, want image/webp", mime)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(output))
	if err != nil {
		t.Fatalf("WebP output cannot be decoded: %v", err)
	}
	if config.Width != 330 || config.Height != 168 {
		t.Fatalf("WebP dimensions = %dx%d, want 330x168", config.Width, config.Height)
	}
}
