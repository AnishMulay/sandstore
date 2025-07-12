package log_service

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
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

func formatLog(level string, event LogEvent) string {
	ts := event.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	meta := ""
	for k, v := range event.Metadata {
		meta += fmt.Sprintf("%s=%v ", k, v)
	}

	return fmt.Sprintf("%s [%s] %s: %s %s\n", ts.Format(time.RFC3339), event.NodeID, level, event.Message, meta)
}

func (ls *LocalDiscLogService) log(level string, event LogEvent) {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	event.NodeID = ls.nodeID

	logMessage := formatLog(level, event)
	ls.logger.Print(logMessage)
}

func (ls *LocalDiscLogService) Debug(event LogEvent) {
	ls.log(DebugLevel, event)
}

func (ls *LocalDiscLogService) Info(event LogEvent) {
	ls.log(InfoLevel, event)
}

func (ls *LocalDiscLogService) Warn(event LogEvent) {
	ls.log(WarnLevel, event)
}

func (ls *LocalDiscLogService) Error(event LogEvent) {
	ls.log(ErrorLevel, event)
}
