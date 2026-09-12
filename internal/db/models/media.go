package models

import (
	"path"
	"strings"
	"time"

	"github.com/zergolf1994/goose"
)

// Media mirrors packages/db/src/models/file-media.model.ts, the model exported
// by packages/db/src/models/index.ts.
type Media struct {
	ID        string      `bson:"_id" json:"id" goose:"required,default:uuid"`
	Type      string      `bson:"type" json:"type"`
	FileID    string      `bson:"fileId" json:"fileId" goose:"required,index"`
	StorageID string      `bson:"storageId" json:"storageId" goose:"required,index"`
	Quality   *string     `bson:"quality,omitempty" json:"quality,omitempty"`
	Key       string      `bson:"key" json:"key" goose:"required"`
	Mime      string      `bson:"mime" json:"mime" goose:"required"`
	Size      interface{} `bson:"size,omitempty" json:"size,omitempty"`
	Width     *int        `bson:"width,omitempty" json:"width,omitempty"`
	Height    *int        `bson:"height,omitempty" json:"height,omitempty"`
	Slug      string      `bson:"slug" json:"slug" goose:"required,unique"`
	CreatedAt time.Time   `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time   `bson:"updatedAt" json:"updatedAt"`
}

func (m Media) ObjectPath() string        { return strings.TrimLeft(strings.TrimSpace(m.Key), "/") }
func (m Media) EffectiveFileID() string   { return strings.TrimSpace(m.FileID) }
func (m Media) EffectiveFileName() string { return path.Base(m.ObjectPath()) }

var MediaModel = goose.NewModel[Media]("medias")
