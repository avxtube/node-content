package handlers

import (
	"node-content/internal/db/models"
	"node-content/internal/services"
)

type proxyDestination struct {
	URL string `json:"url"`
}

func getOnlineStorage(storageID string) (models.Storage, bool) {
	storage, ok := services.GetStorage(storageID)
	return storage, ok && storage.IsOnline()
}
