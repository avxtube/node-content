package models

import (
	"net/url"
	"strings"
	"time"

	"github.com/zergolf1994/goose"
)

// Storage mirrors packages/db/src/models/storage.model.ts.
type StorageLocalConfig struct {
	BasePath string `bson:"basePath" json:"basePath"`
}

type StorageS3Config struct {
	Endpoint                 string `bson:"endpoint,omitempty" json:"endpoint,omitempty"`
	Region                   string `bson:"region" json:"region"`
	Bucket                   string `bson:"bucket" json:"bucket"`
	Prefix                   string `bson:"prefix,omitempty" json:"prefix,omitempty"`
	ForcePathStyle           bool   `bson:"forcePathStyle" json:"forcePathStyle"`
	CredentialsConfigured    bool   `bson:"credentialsConfigured" json:"credentialsConfigured"`
	AccessKeyIDEncrypted     string `bson:"accessKeyIdEncrypted,omitempty" json:"-"`
	SecretAccessKeyEncrypted string `bson:"secretAccessKeyEncrypted,omitempty" json:"-"`
}

type StorageHealth struct {
	CheckedAt *time.Time `bson:"checkedAt,omitempty" json:"checkedAt,omitempty"`
	LatencyMS *float64   `bson:"latencyMs,omitempty" json:"latencyMs,omitempty"`
	Message   *string    `bson:"message,omitempty" json:"message,omitempty"`
}

type StorageCapacity struct {
	TotalBytes *int64 `bson:"totalBytes,omitempty" json:"totalBytes,omitempty"`
	UsedBytes  *int64 `bson:"usedBytes,omitempty" json:"usedBytes,omitempty"`
	FreeBytes  *int64 `bson:"freeBytes,omitempty" json:"freeBytes,omitempty"`
}

type Storage struct {
	ID        string              `bson:"_id" json:"id" goose:"required,default:uuid"`
	Name      string              `bson:"name" json:"name"`
	Provider  string              `bson:"provider" json:"provider"`
	Enabled   bool                `bson:"enabled" json:"enabled"`
	Priority  int                 `bson:"priority" json:"priority"`
	Purposes  []string            `bson:"purposes" json:"purposes"`
	Kinds     []string            `bson:"kinds" json:"kinds"`
	PublicURL string              `bson:"publicUrl,omitempty" json:"publicUrl,omitempty"`
	OriginURL string              `bson:"originUrl,omitempty" json:"originUrl,omitempty"`
	Local     *StorageLocalConfig `bson:"local,omitempty" json:"local,omitempty"`
	S3        *StorageS3Config    `bson:"s3,omitempty" json:"s3,omitempty"`
	Status    string              `bson:"status" json:"status"`
	Health    *StorageHealth      `bson:"health,omitempty" json:"health,omitempty"`
	Capacity  *StorageCapacity    `bson:"capacity,omitempty" json:"capacity,omitempty"`
	CreatedBy string              `bson:"createdBy" json:"createdBy"`
	UpdatedBy string              `bson:"updatedBy" json:"updatedBy"`
	DeletedAt *time.Time          `bson:"deletedAt,omitempty" json:"deletedAt,omitempty"`
	CreatedAt time.Time           `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time           `bson:"updatedAt" json:"updatedAt"`
}

var StorageModel = goose.NewModel[Storage]("storages")

func (s *Storage) GetPublicBaseURL() string {
	for _, raw := range strings.Split(s.PublicURL, ",") {
		if normalized := normalizeBaseURL(raw, "https"); normalized != "" {
			return normalized
		}
	}
	return ""
}

func (s *Storage) GetOriginBaseURL() string { return normalizeBaseURL(s.OriginURL, "https") }

func (s *Storage) GetS3ObjectBaseURL() string {
	if public := s.GetPublicBaseURL(); public != "" {
		return public
	}
	if origin := s.GetOriginBaseURL(); origin != "" {
		return origin
	}
	if s.S3 == nil || strings.TrimSpace(s.S3.Endpoint) == "" || strings.TrimSpace(s.S3.Bucket) == "" {
		return ""
	}
	base, err := url.JoinPath(normalizeBaseURL(s.S3.Endpoint, "https"), strings.TrimSpace(s.S3.Bucket))
	if err != nil {
		return ""
	}
	return strings.TrimRight(base, "/")
}

func (s *Storage) GetPublicDomains() []string {
	var domains []string
	for _, raw := range strings.Split(s.PublicURL, ",") {
		parsed, err := url.Parse(normalizeBaseURL(raw, "https"))
		if err == nil && parsed.Host != "" {
			domains = append(domains, parsed.Host)
		}
	}
	return domains
}

// node-storage owns both local and S3 delivery, so every provider is reached
// through the configured publicUrl.
func (s *Storage) GetPlaybackBaseURL() string { return s.GetPublicBaseURL() }
func (s *Storage) GetStorageBaseURL() string  { return s.GetPublicBaseURL() }
func (s *Storage) GetVODBaseURL() string      { return s.GetPublicBaseURL() }

func (s *Storage) IsProxy() bool { return strings.EqualFold(strings.TrimSpace(s.Provider), "proxy") }

func normalizeBaseURL(raw, defaultScheme string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = defaultScheme + "://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func (s *Storage) IsOnline() bool {
	if !s.Enabled || s.DeletedAt != nil {
		return false
	}
	if s.IsProxy() {
		if s.GetPublicBaseURL() == "" {
			return false
		}
		// A newly-created proxy can be ready for delivery before its first
		// health check changes the persisted status from unknown to online.
		return s.Status == "" || s.Status == "unknown" || s.Status == "online"
	}
	return s.Status == "online"
}
