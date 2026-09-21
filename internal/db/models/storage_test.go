package models

import "testing"

func TestS3StorageURLs(t *testing.T) {
	publicURL := "cdn-a.example.com, https://cdn-b.example.com/base/"
	originURL := "origin.example.com/raw/"
	storage := &Storage{Provider: "s3", PublicURL: publicURL, OriginURL: originURL}

	if got, want := storage.GetPlaybackBaseURL(), "https://cdn-a.example.com"; got != want {
		t.Fatalf("GetPlaybackBaseURL() = %q, want %q", got, want)
	}
	domains := storage.GetPublicDomains()
	if len(domains) != 2 || domains[0] != "cdn-a.example.com" || domains[1] != "cdn-b.example.com" {
		t.Fatalf("GetPublicDomains() = %#v", domains)
	}
	if got, want := storage.GetVODBaseURL(), "https://cdn-a.example.com"; got != want {
		t.Fatalf("GetVODBaseURL() = %q, want %q", got, want)
	}
	if got, want := storage.GetOriginBaseURL(), "https://origin.example.com/raw"; got != want {
		t.Fatalf("GetOriginBaseURL() = %q, want %q", got, want)
	}
}

func TestLocalStorageURLs(t *testing.T) {
	storage := &Storage{
		Provider:  "local",
		PublicURL: "http://10.0.0.8:8888",
		Local:     &StorageLocalConfig{BasePath: "/srv/media"},
	}

	if got, want := storage.GetPlaybackBaseURL(), "http://10.0.0.8:8888"; got != want {
		t.Fatalf("GetPlaybackBaseURL() = %q, want %q", got, want)
	}
	if got, want := storage.GetStorageBaseURL(), "http://10.0.0.8:8888"; got != want {
		t.Fatalf("GetStorageBaseURL() = %q, want %q", got, want)
	}
	if got, want := storage.GetVODBaseURL(), "http://10.0.0.8:8888"; got != want {
		t.Fatalf("GetVODBaseURL() = %q, want %q", got, want)
	}
}

func TestPublicURLRejectsInvalidValue(t *testing.T) {
	publicURL := "://invalid"
	storage := &Storage{Provider: "s3", PublicURL: publicURL}
	if got := storage.GetPublicBaseURL(); got != "" {
		t.Fatalf("GetPublicBaseURL() = %q, want empty", got)
	}
}

func TestProxyStorageUsesPublicURL(t *testing.T) {
	storage := &Storage{
		Provider:  "proxy",
		Enabled:   true,
		Status:    "unknown",
		PublicURL: "https://proxy.example.com/base/",
		OriginURL: "https://origin.example.com/ignored",
	}
	if !storage.IsProxy() {
		t.Fatal("proxy provider was not detected")
	}
	if got, want := storage.GetPlaybackBaseURL(), "https://proxy.example.com/base"; got != want {
		t.Fatalf("GetPlaybackBaseURL() = %q, want %q", got, want)
	}
	if !storage.IsOnline() {
		t.Fatal("enabled proxy with a public URL must be usable before its first health check")
	}
	storage.Status = "error"
	if storage.IsOnline() {
		t.Fatal("proxy with an explicit error status must not be usable")
	}
}
