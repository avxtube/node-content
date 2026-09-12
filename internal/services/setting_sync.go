package services

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"node-content/internal/db/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// executableDir returns the directory of the current executable.
func executableDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

func settingFilePath() string {
	exe, err := executableDir()
	if err != nil {
		log.Printf("⚠️ Cannot get executable path: %v", err)
		return filepath.Join("conf", "setting.json")
	}
	return filepath.Join(exe, "conf", "setting.json")
}

// ─── Atomic File Write Helper ────────────────────────────────────────

// writeJSONFile writes data to a conf/ file atomically (tmp → rename).
func writeJSONFile(filePath string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}
	tmpPath := filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		return os.WriteFile(filePath, data, 0644)
	}
	return nil
}

// ─── Settings Sync ───────────────────────────────────────────────────

// SyncSettings snapshots shared settings and safe storage delivery fields.
func SyncSettings() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	settingNames := []string{"player_maintenance", "advert_hobby", "domain_setting", "domain_content", "domain_playlist", "domain_preview", "domain_static"}
	cursor, err := models.SettingModel.Col().Find(ctx, bson.M{
		"name": bson.M{"$in": settingNames},
	})
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	result := make(map[string]interface{})
	for cursor.Next(ctx) {
		var raw bson.M
		if err := cursor.Decode(&raw); err != nil {
			log.Printf("⚠️ Failed to decode setting: %v", err)
			continue
		}
		name, _ := raw["name"].(string)
		value := raw["value"]
		if name != "" {
			result[name] = value
			if name == "domain_setting" {
				// custom_domains was removed from the current platform schema.
				// Keep the active domain_setting under an explicit snapshot key.
				result["custom_domain"] = value
			}
		}
	}
	if err := cursor.Err(); err != nil {
		return err
	}

	storageCursor, err := models.StorageModel.Col().Find(
		ctx,
		bson.M{"deletedAt": nil},
		options.Find().SetProjection(bson.M{
			"_id": 1, "name": 1, "provider": 1, "enabled": 1,
			"priority": 1, "purposes": 1, "kinds": 1,
			"publicUrl": 1, "originUrl": 1, "status": 1,
			"deletedAt": 1, "updatedAt": 1,
		}),
	)
	if err != nil {
		return err
	}
	defer storageCursor.Close(ctx)

	storages := make([]storageSnapshot, 0)
	for storageCursor.Next(ctx) {
		var storage storageSnapshot
		if err := storageCursor.Decode(&storage); err != nil {
			log.Printf("⚠️ Failed to decode storage snapshot: %v", err)
			continue
		}
		storages = append(storages, storage)
	}
	if err := storageCursor.Err(); err != nil {
		return err
	}
	result["storages"] = storages

	if err := writeJSONFile(settingFilePath(), result); err != nil {
		return err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(raw, &settings); err != nil {
		return err
	}
	installRuntimeSnapshot(settings)
	return nil
}

// ─── Scheduler ───────────────────────────────────────────────────────

// StartSettingSyncScheduler refreshes the snapshot at the next exact minute.
func StartSettingSyncScheduler(ctx context.Context) {
	syncAll := func() {
		if err := SyncSettings(); err != nil {
			log.Printf("⚠️ Failed to sync settings: %v", err)
		}
	}

	syncAll()

	for {
		// Calculate time until the next exact minute (00 second)
		now := time.Now()
		next := now.Truncate(time.Minute).Add(time.Minute)
		duration := time.Until(next)

		select {
		case <-ctx.Done():
			log.Println("⏹️ Settings sync scheduler stopped")
			return
		case <-time.After(duration):
			syncAll()
		}
	}
}
