package handlers

import "node-content/internal/core/enums"

func playableFileStatuses() []string {
	return []string{
		enums.FileStatusReady,
		enums.FileStatusReadyOriginal,
	}
}

func isPlayableFileStatus(status string) bool {
	return status == enums.FileStatusReady || status == enums.FileStatusReadyOriginal
}
