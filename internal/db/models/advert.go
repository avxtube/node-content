package models

// AdContent is one advert entry stored inside settings.advert_hobby.
type AdContent struct {
	ID          string   `bson:"_id,omitempty" json:"_id,omitempty"`
	Enabled     bool     `bson:"enabled" json:"enabled"`
	Name        string   `bson:"name" json:"name"`
	MP4URL      *string  `bson:"mp4Url,omitempty" json:"mp4Url,omitempty"`
	SkipSeconds *int     `bson:"skipSeconds,omitempty" json:"skipSeconds,omitempty"`
	ImageURL    *string  `bson:"imageUrl,omitempty" json:"imageUrl,omitempty"`
	ShowOn      []string `bson:"showOn,omitempty" json:"showOn,omitempty"`
	WebsiteURL  *string  `bson:"websiteUrl,omitempty" json:"websiteUrl,omitempty"`
	Script      *string  `bson:"script,omitempty" json:"script,omitempty"`
}

type AdvertCategory struct {
	Enabled bool        `bson:"enabled" json:"enabled"`
	List    []AdContent `bson:"list" json:"list"`
}

// Adverts is the shape of the advert_hobby setting.
type Adverts struct {
	Video  AdvertCategory `bson:"video" json:"video"`
	Image  AdvertCategory `bson:"image" json:"image"`
	Script AdvertCategory `bson:"script" json:"script"`
}
