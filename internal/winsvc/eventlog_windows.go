//go:build windows

package winsvc

import "golang.org/x/sys/windows/svc/eventlog"

// Event ids: EventCreate.exe, the message file the source is registered with
// (FR-026), renders the text of any id from 1 to 1000 verbatim.
const (
	eventIDInfo    = 1
	eventIDWarning = 2
	eventIDError   = 3
)

// EventLog is a Sink writing to the Application log as EventSource.
type EventLog struct{ l *eventlog.Log }

// OpenEventLog opens the Application log for EventSource. The source must have
// been registered by `service install`.
func OpenEventLog() (*EventLog, error) {
	l, err := eventlog.Open(EventSource)
	if err != nil {
		return nil, err
	}
	return &EventLog{l: l}, nil
}

// Info implements Sink.
func (e *EventLog) Info(msg string) error { return e.l.Info(eventIDInfo, msg) }

// Warning implements Sink.
func (e *EventLog) Warning(msg string) error { return e.l.Warning(eventIDWarning, msg) }

// Error implements Sink.
func (e *EventLog) Error(msg string) error { return e.l.Error(eventIDError, msg) }

// Close releases the log handle.
func (e *EventLog) Close() error { return e.l.Close() }
