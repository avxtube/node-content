package services

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"node-content/internal/config"
	"node-content/internal/db/models"
)

var runtimeSnapshot = struct {
	sync.RWMutex
	settings map[string]json.RawMessage
	storages map[string]models.Storage
}{
	settings: make(map[string]json.RawMessage),
	storages: make(map[string]models.Storage),
}

type storageSnapshot struct {
	ID        string     `bson:"_id" json:"_id"`
	Name      string     `bson:"name" json:"name,omitempty"`
	Provider  string     `bson:"provider" json:"provider"`
	Enabled   bool       `bson:"enabled" json:"enabled"`
	Priority  int        `bson:"priority" json:"priority,omitempty"`
	Purposes  []string   `bson:"purposes" json:"purposes,omitempty"`
	Kinds     []string   `bson:"kinds" json:"kinds,omitempty"`
	PublicURL string     `bson:"publicUrl" json:"publicUrl,omitempty"`
	OriginURL string     `bson:"originUrl" json:"originUrl,omitempty"`
	Status    string     `bson:"status" json:"status"`
	DeletedAt *time.Time `bson:"deletedAt" json:"deletedAt,omitempty"`
	UpdatedAt time.Time  `bson:"updatedAt" json:"updatedAt,omitempty"`
}

// ReadSettingFile reads and parses conf/setting.json.
// Returns a map of setting name → raw JSON bytes.
func ReadSettingFile() (map[string]json.RawMessage, error) {
	runtimeSnapshot.RLock()
	if len(runtimeSnapshot.settings) > 0 {
		settings := cloneRawSettings(runtimeSnapshot.settings)
		runtimeSnapshot.RUnlock()
		return settings, nil
	}
	runtimeSnapshot.RUnlock()

	data, err := os.ReadFile(settingFilePath())
	if err != nil {
		return nil, err
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}
	installRuntimeSnapshot(settings)
	return settings, nil
}

func cloneRawSettings(source map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
}

func installRuntimeSnapshot(settings map[string]json.RawMessage) {
	storages := make(map[string]models.Storage)
	if raw := settings["storages"]; len(raw) > 0 {
		var values []storageSnapshot
		if json.Unmarshal(raw, &values) == nil {
			for _, value := range values {
				if value.ID == "" {
					continue
				}
				storages[value.ID] = models.Storage{
					ID: value.ID, Name: value.Name, Provider: value.Provider,
					Enabled: value.Enabled, Priority: value.Priority,
					Purposes: value.Purposes, Kinds: value.Kinds,
					PublicURL: value.PublicURL, OriginURL: value.OriginURL,
					Status: value.Status, DeletedAt: value.DeletedAt,
					UpdatedAt: value.UpdatedAt,
				}
			}
		}
	}

	runtimeSnapshot.Lock()
	runtimeSnapshot.settings = cloneRawSettings(settings)
	runtimeSnapshot.storages = storages
	runtimeSnapshot.Unlock()
}

// GetStorage returns the latest safe storage snapshot without querying MongoDB.
func GetStorage(id string) (models.Storage, bool) {
	runtimeSnapshot.RLock()
	storage, ok := runtimeSnapshot.storages[strings.TrimSpace(id)]
	runtimeSnapshot.RUnlock()
	return storage, ok
}

// ─── Database Setting Helpers ─────────────────────────────────────────

// getStringSetting is a helper to read a string setting from conf/setting.json
func getStringSetting(key string) string {
	settings, err := ReadSettingFile()
	if err != nil {
		return ""
	}
	if raw, exists := settings[key]; exists {
		var val string
		if err := json.Unmarshal(raw, &val); err == nil && val != "" {
			return val
		}
	}

	// The current platform stores domain values together in domain_setting.
	for _, groupKey := range []string{"custom_domain", "domain_setting"} {
		var values map[string]string
		if groupRaw := settings[groupKey]; len(groupRaw) > 0 && json.Unmarshal(groupRaw, &values) == nil {
			if value := values[key]; value != "" {
				return value
			}
		}
	}
	return ""
}

// GetDomainContent fetches the domain_content setting. Fallbacks to fallbackHost.
func GetDomainContent(fallbackHost string) string {
	val := getStringSetting("domain_content")
	if val != "" {
		return val
	}
	return fallbackHost
}

// GetDomainPlaylist fetches the domain_playlist setting. Fallbacks to domain_content, then fallbackHost.
func GetDomainPlaylist(fallbackHost string) string {
	val := getStringSetting("domain_playlist")
	if val != "" {
		return val
	}
	return GetDomainContent(fallbackHost)
}

// GetDomainAds fetches the domain_ads setting. Fallbacks to domain_content, then fallbackHost.
// func GetDomainAds(fallbackHost string) string {
// 	val := getStringSetting("domain_ads")
// 	if val != "" {
// 		return val
// 	}
// 	return GetDomainContent(fallbackHost)
// }

// GetDomainPreview fetches the domain_preview setting. Returns empty if not set.
func GetDomainPreview() string {
	return normalizeDomainHost(getStringSetting("domain_preview"))
}

// GetDomainStatic fetches the domain_static setting.
// Synced setting.json takes priority; DOMAIN_STATIC env is a dev fallback only.
func GetDomainStatic() string {
	if val := normalizeDomainHost(getStringSetting("domain_static")); val != "" {
		return val
	}
	return normalizeDomainHost(config.AppConfig.DomainStatic)
}

// normalizeDomainHost strips scheme/trailing slash from a host setting value.
func normalizeDomainHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "https://") {
		host = strings.TrimPrefix(host, "https://")
	} else if strings.HasPrefix(host, "http://") {
		host = strings.TrimPrefix(host, "http://")
	}
	return strings.TrimRight(host, "/")
}
