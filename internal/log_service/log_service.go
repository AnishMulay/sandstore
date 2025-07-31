package log_service

import "time"

const (
	DebugLevel = "DEBUG"
	InfoLevel  = "INFO"
	WarnLevel  = "WARN"
	ErrorLevel = "ERROR"
)

const (
	DebugLevelValue = 1
	InfoLevelValue  = 2
	WarnLevelValue  = 3
	ErrorLevelValue = 4
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

func GetLevelValue(level string) int {
	switch level {
	case DebugLevel:
		return DebugLevelValue
	case InfoLevel:
		return InfoLevelValue
	case WarnLevel:
		return WarnLevelValue
	case ErrorLevel:
		return ErrorLevelValue
	default:
		return InfoLevelValue // Default to INFO if unknown
	}
}
