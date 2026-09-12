package handlers

import (
	"strings"
	"testing"

	"node-content/internal/db/models"
)

func TestWriteAudioRenditionsCreatesOneDefaultTrack(t *testing.T) {
	medias := []models.Media{
		{Slug: "audio-1"},
		{Slug: "audio-2"},
	}
	var playlist strings.Builder
	writeAudioRenditions(&playlist, "hls.example.com", medias)
	result := playlist.String()
	if strings.Count(result, "#EXT-X-MEDIA:TYPE=AUDIO") != 2 {
		t.Fatalf("unexpected audio renditions: %s", result)
	}
	if strings.Count(result, "DEFAULT=YES") != 1 {
		t.Fatalf("want exactly one default audio track: %s", result)
	}
	if !strings.Contains(result, `URI="//hls.example.com/audio-1/audio.m3u8"`) {
		t.Fatalf("missing audio URI: %s", result)
	}
}

func TestAttachAudioGroupAddsAudioCodecAndIsIdempotent(t *testing.T) {
	input := `#EXT-X-STREAM-INF:BANDWIDTH=500000,CODECS="avc1.64001f"`
	got := attachAudioGroup(input)
	if !strings.Contains(got, `CODECS="avc1.64001f,mp4a.40.2"`) || !strings.Contains(got, `AUDIO="audio"`) {
		t.Fatalf("unexpected stream info: %s", got)
	}
	if twice := attachAudioGroup(got); twice != got {
		t.Fatalf("attachAudioGroup is not idempotent: %s", twice)
	}
}
