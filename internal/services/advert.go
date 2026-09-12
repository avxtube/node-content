package services

import (
	"encoding/json"
	"log"

	"node-content/internal/core/enums"
	"node-content/internal/db/models"
)

// ─── Advert Hobby (setting advert_hobby) ─────────────────────────────
// admin เก็บเป็น {video, image, script} ใน settings.advert_hobby

// GetAdvertHobby reads advert_hobby from setting.json as embedded advert
// objects (used by /vast/hobby.xml and /advert/hobby.json).
func GetAdvertHobby() *models.Adverts {
	settings, err := ReadSettingFile()
	if err != nil {
		return nil
	}
	raw, exists := settings[enums.SettingAdvertHobby]
	if !exists {
		return nil
	}
	var result models.Adverts
	if err := json.Unmarshal(raw, &result); err != nil {
		log.Printf("⚠️ Cannot parse advert_hobby: %v", err)
		return nil
	}
	return &result
}

// ─── Ad Slug / Feed Resolution ────────────────────────────────────────

// ResolveAdSlug returns the advert feed supported by the current schema.
func ResolveAdSlug() string {
	return "hobby"
}

// ResolveAdvertsBySlug loads advert config for hobby or a domain slug.
// The current schema has only the global hobby advert setting.
func ResolveAdvertsBySlug(adSlug string) *models.Adverts {
	if adSlug == "hobby" {
		return GetAdvertHobby()
	}
	return nil
}

// BuildAdvertFeed builds /advert/{adSlug}.json (script + image + video).
func BuildAdvertFeed(adSlug string) *models.Adverts {
	adverts := ResolveAdvertsBySlug(adSlug)
	if adverts != nil {
		return adverts
	}
	empty := models.Adverts{}
	return &empty
}
