package models

import (
	"time"

	"github.com/zergolf1994/goose"
)

// VideoProcess matches platform/packages/db/src/models/file-process.model.ts.
type VideoProcess struct {
	ID                   string      `bson:"_id" json:"id"`
	FileID               string      `bson:"fileId" json:"fileId"`
	ProcessType          string      `bson:"processType" json:"processType"`
	DedupeKey            string      `bson:"dedupeKey" json:"dedupeKey"`
	Status               string      `bson:"status" json:"status"`
	Priority             int         `bson:"priority" json:"priority"`
	WorkerID             *string     `bson:"workerId,omitempty" json:"workerId,omitempty"`
	TargetStorageID      *string     `bson:"targetStorageId,omitempty" json:"targetStorageId,omitempty"`
	DestinationStorageID *string     `bson:"destinationStorageId,omitempty" json:"destinationStorageId,omitempty"`
	TransferMode         *string     `bson:"transferMode,omitempty" json:"transferMode,omitempty"`
	SourceStorageID      *string     `bson:"sourceStorageId,omitempty" json:"sourceStorageId,omitempty"`
	TempStorageID        *string     `bson:"tempStorageId,omitempty" json:"tempStorageId,omitempty"`
	MigrationID          *string     `bson:"migrationId,omitempty" json:"migrationId,omitempty"`
	SourceMediaIDs       []string    `bson:"sourceMediaIds,omitempty" json:"sourceMediaIds,omitempty"`
	ClaimedAt            *time.Time  `bson:"claimedAt,omitempty" json:"claimedAt,omitempty"`
	HeartbeatAt          *time.Time  `bson:"heartbeatAt,omitempty" json:"heartbeatAt,omitempty"`
	LeaseExpiresAt       *time.Time  `bson:"leaseExpiresAt,omitempty" json:"leaseExpiresAt,omitempty"`
	StartedAt            *time.Time  `bson:"startedAt,omitempty" json:"startedAt,omitempty"`
	FinishedAt           *time.Time  `bson:"finishedAt,omitempty" json:"finishedAt,omitempty"`
	NextRetryAt          *time.Time  `bson:"nextRetryAt,omitempty" json:"nextRetryAt,omitempty"`
	OverallPercent       float64     `bson:"overallPercent" json:"overallPercent"`
	Timeline             interface{} `bson:"timeline,omitempty" json:"timeline,omitempty"`
	FileName             *string     `bson:"file_name,omitempty" json:"fileName,omitempty"`
	FileSize             interface{} `bson:"file_size,omitempty" json:"fileSize,omitempty"`
	Resolution           *string     `bson:"resolution,omitempty" json:"resolution,omitempty"`
	SourceType           *string     `bson:"sourceType,omitempty" json:"sourceType,omitempty"`
	M3U8URL              *string     `bson:"m3u8_url,omitempty" json:"m3u8Url,omitempty"`
	Resolutions          []string    `bson:"resolutions,omitempty" json:"resolutions,omitempty"`
	Completed            []string    `bson:"completed,omitempty" json:"completed,omitempty"`
	Error                *string     `bson:"error,omitempty" json:"error,omitempty"`
	ErrorCategory        *string     `bson:"errorCategory,omitempty" json:"errorCategory,omitempty"`
	RetryCount           int         `bson:"retryCount" json:"retryCount"`
	CreatedAt            time.Time   `bson:"createdAt" json:"createdAt"`
	UpdatedAt            time.Time   `bson:"updatedAt" json:"updatedAt"`
}

var VideoProcessModel = goose.NewModel[VideoProcess]("video_process")
