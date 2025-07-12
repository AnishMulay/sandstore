package log_service

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

type LocalDiscLogService struct {
	logDir string
	nodeID string
	mu     sync.Mutex
	logger *log.Logger
}

func NewLocalDiscLogService(logDir string, nodeID string) *LocalDiscLogService {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Fatalf("failed to create log directory: %v", err)
	}

	filePath := filepath.Join(logDir, fmt.Sprintf("%s.log", nodeID))
	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("failed to open log file: %v", err)
	}

	return &LocalDiscLogService{
		logDir: logDir,
		nodeID: nodeID,
		logger: log.New(file, "", 0),
	}
}
