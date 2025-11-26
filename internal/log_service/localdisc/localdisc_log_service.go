package localdisc

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/log_service"
)

type LocalDiscLogService struct {
	logDir        string
	nodeID        string
	mu            sync.Mutex
	logger        *log.Logger
	minLevel      int
	filterEnabled bool
}

func NewLocalDiscLogService(logDir string, nodeID string, minLogLevel ...string) *LocalDiscLogService {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Fatalf("failed to create log directory: %v", err)
	}

	filePath := filepath.Join(logDir, fmt.Sprintf("%s.log", nodeID))
	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("failed to open log file: %v", err)
	}

	service := &LocalDiscLogService{
		logDir:        logDir,
		nodeID:        nodeID,
		logger:        log.New(file, "", 0),
		filterEnabled: true, // Always filter by default
		minLevel:      log_service.DebugLevelValue,
	}

	if len(minLogLevel) > 0 && minLogLevel[0] != "" {
		service.SetMinLogLevel(minLogLevel[0])
	}

	return service
}

func (ls *LocalDiscLogService) SetMinLogLevel(level string) {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	normalizedLevel := strings.ToUpper(strings.TrimSpace(level))
	ls.minLevel = log_service.GetLevelValue(normalizedLevel)
	ls.filterEnabled = true
}

func (ls *LocalDiscLogService) DisableFiltering() {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.filterEnabled = false
}

func (ls *LocalDiscLogService) shouldLog(level string) bool {
	if !ls.filterEnabled {
		return true
	}
	levelValue := log_service.GetLevelValue(level)
	return levelValue >= ls.minLevel
}

func formatLog(level string, event log_service.LogEvent) string {
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

func (ls *LocalDiscLogService) log(level string, event log_service.LogEvent) {
	if !ls.shouldLog(level) {
		return
	}

	ls.mu.Lock()
	defer ls.mu.Unlock()

	event.NodeID = ls.nodeID
	logMessage := formatLog(level, event)
	ls.logger.Print(logMessage)
}

func (ls *LocalDiscLogService) Debug(event log_service.LogEvent) {
	ls.log(log_service.DebugLevel, event)
}

func (ls *LocalDiscLogService) Info(event log_service.LogEvent) {
	ls.log(log_service.InfoLevel, event)
}

func (ls *LocalDiscLogService) Warn(event log_service.LogEvent) {
	ls.log(log_service.WarnLevel, event)
}

func (ls *LocalDiscLogService) Error(event log_service.LogEvent) {
	ls.log(log_service.ErrorLevel, event)
}

var _ log_service.LogService = (*LocalDiscLogService)(nil)
