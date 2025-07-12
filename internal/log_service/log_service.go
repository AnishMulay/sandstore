package log_service

import "time"

const (
	DebugLevel = "DEBUG"
	InfoLevel  = "INFO"
	WarnLevel  = "WARN"
	ErrorLevel = "ERROR"
)

type LogEvent struct {
	Timestamp time.Time
	NodeID    string
	Message   string
	Metadata  map[string]any
}

type LogService interface {
	Debug(event LogEvent)
	Info(event LogEvent)
	Warn(event LogEvent)
	Error(event LogEvent)
}
