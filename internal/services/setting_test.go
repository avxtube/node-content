package services

import (
	"encoding/json"
	"testing"

	"node-content/internal/db/models"
)

func TestRuntimeSnapshotProvidesDomainsAndStorages(t *testing.T) {
	runtimeSnapshot.Lock()
	previousSettings := runtimeSnapshot.settings
	previousStorages := runtimeSnapshot.storages
	runtimeSnapshot.settings = make(map[string]json.RawMessage)
	runtimeSnapshot.storages = make(map[string]models.Storage)
	runtimeSnapshot.Unlock()
	t.Cleanup(func() {
		runtimeSnapshot.Lock()
		runtimeSnapshot.settings = previousSettings
		runtimeSnapshot.storages = previousStorages
		runtimeSnapshot.Unlock()
	})

	settings := map[string]json.RawMessage{
		"custom_domain": json.RawMessage(`{"domain_content":"content.example","domain_playlist":"playlist.example","domain_static":"static.example"}`),
		"storages":      json.RawMessage(`[{"_id":"storage-1","name":"primary","provider":"s3","enabled":true,"publicUrl":"https://media.example","originUrl":"https://origin.example","status":"online"}]`),
	}
	installRuntimeSnapshot(settings)

	if got := GetDomainContent("fallback.example"); got != "content.example" {
		t.Fatalf("content domain = %q", got)
	}
	if got := GetDomainPlaylist("fallback.example"); got != "playlist.example" {
		t.Fatalf("playlist domain = %q", got)
	}
	storage, ok := GetStorage("storage-1")
	if !ok {
		t.Fatal("storage snapshot was not found")
	}
	if storage.PublicURL != "https://media.example" || storage.OriginURL != "https://origin.example" || !storage.IsOnline() {
		t.Fatalf("unexpected storage snapshot: %#v", storage)
	}
}
