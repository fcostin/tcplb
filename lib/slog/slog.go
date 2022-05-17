// Package slog is a logger interface offering a uniformly unpleasant
// and wearying experience for application developers, users and operators.
//
// TODO replace this entirely with something else. Maybe zerolog?
package slog

import (
	"encoding/json"
	"fmt"
	"log"
	"tcplb/lib/core"
)

// LogRecord holds data for a single server log record.
type LogRecord struct {
	Msg        string         `json:"msg,omitempty"`        // Msg is an optional log message
	Error      error          `json:"error,omitempty"`      // Error is an optional error
	Details    any            `json:"details,omitempty"`    // Details are optional details
	StackTrace string         `json:"stacktrace,omitempty"` // StackTrace is optional stack trace
	ClientID   *core.ClientID `json:"clientid,omitempty"`   // ClientID is optional id of client, if known.
	Upstream   *core.Upstream `json:"upstream,omitempty"`   // Upstream is optional upstream, if known.
}

// Logger is an abstract log interface for the server.
//
// Multiple goroutines may invoke methods on a Logger simultaneously.
type Logger interface {
	Info(record *LogRecord)
	Warn(record *LogRecord)
	Error(record *LogRecord)
}

// TODO make the log output less awful to read by humans and machines.
type stdlibLogShim struct{}

type errorPayload struct {
	Type  string `json:"type,omitempty"`  // Type is the error type
	Error string `json:"error,omitempty"` // Error is the error message
}

func asErrorPayload(err error) *errorPayload {
	if err == nil {
		return nil
	}
	return &errorPayload{
		Type:  fmt.Sprintf("%T", err),
		Error: err.Error(),
	}
}

type recordPayload struct {
	Msg        string         `json:"msg,omitempty"`        // Msg is an optional log message
	Error      *errorPayload  `json:"error,omitempty"`      // Error is an optional error
	Details    any            `json:"details,omitempty"`    // Details are optional details
	StackTrace string         `json:"stacktrace,omitempty"` // StackTrace is optional stack trace
	ClientID   *core.ClientID `json:"clientid,omitempty"`   // ClientID is optional id of client, if known.
	Upstream   *core.Upstream `json:"upstream,omitempty"`   // Upstream is optional upstream, if known.
	Level      string         `json:"level,omitempty"`
}

func logRecordAsSemiJSON(level string, record *LogRecord) {
	var payload recordPayload
	payload.Level = level
	if record != nil {
		payload.Msg = record.Msg
		payload.Error = asErrorPayload(record.Error)
		payload.Details = record.Details
		payload.StackTrace = record.StackTrace
		payload.ClientID = record.ClientID
		payload.Upstream = record.Upstream
	}

	data, _ := json.Marshal(&payload)

	// TODO put the timestamps in the JSON as well.
	log.Println(string(data))
}

func (s *stdlibLogShim) Info(record *LogRecord) {
	logRecordAsSemiJSON("info", record)
}

func (s *stdlibLogShim) Warn(record *LogRecord) {
	logRecordAsSemiJSON("warn", record)
}

func (s *stdlibLogShim) Error(record *LogRecord) {
	logRecordAsSemiJSON("error", record)
}

// GetDefaultLogger returns the default Logger.
func GetDefaultLogger() Logger {
	return &stdlibLogShim{}
}

// RecordingLogger captures all logged events in memory.
// It is designed for use as a test fixture.
type RecordingLogger struct {
	Events []Event
}

type Event struct {
	Level string
	*LogRecord
}

func (l *RecordingLogger) Info(record *LogRecord) {
	l.Events = append(l.Events, Event{Level: "info", LogRecord: record})
}

func (l *RecordingLogger) Warn(record *LogRecord) {
	l.Events = append(l.Events, Event{Level: "warn", LogRecord: record})
}

func (l *RecordingLogger) Error(record *LogRecord) {
	l.Events = append(l.Events, Event{Level: "error", LogRecord: record})
}

var _ Logger = (*RecordingLogger)(nil) // type check
