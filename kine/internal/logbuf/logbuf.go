// Package logbuf captures process log lines for the dashboard console viewer
// (/dashboard/console-log), the Go counterpart of src/lib/consoleLogBuffer.js.
//
// It is an io.Writer, so it plugs into log.SetOutput via io.MultiWriter and
// keeps working for every log.Printf in the process without extra call sites.
package logbuf

import (
	"bytes"
	"strings"
	"sync"
)

// MaxLines matches CONSOLE_LOG_CONFIG.maxLines in the dashboard.
const MaxLines = 200

// Event is one message on the console feed: "line" or "clear".
type Event struct {
	Line  string
	Clear bool
}

type buffer struct {
	mu      sync.Mutex
	lines   []string
	subs    map[int]chan Event
	nextID  int
	partial []byte
}

var global = &buffer{subs: map[int]chan Event{}}

// Default returns the process-wide buffer.
func Default() *buffer { return global }

// Write implements io.Writer: complete lines are appended, a trailing partial
// line is held until its newline arrives.
func (b *buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.partial = append(b.partial, p...)
	for {
		i := bytes.IndexByte(b.partial, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(b.partial[:i]), "\r")
		b.partial = b.partial[i+1:]
		if line == "" {
			continue
		}
		b.lines = append(b.lines, line)
		if len(b.lines) > MaxLines {
			b.lines = append([]string(nil), b.lines[len(b.lines)-MaxLines:]...)
		}
		for _, ch := range b.subs {
			select {
			case ch <- Event{Line: line}:
			default: // slow subscriber: drop rather than block logging
			}
		}
	}
	return len(p), nil
}

// Lines returns a copy of the buffered lines (empty slice, never nil, so the
// dashboard always receives an array).
func (b *buffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append(make([]string, 0, len(b.lines)), b.lines...)
}

// Subscribe returns a channel of events and its cancel func.
func (b *buffer) Subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	ch := make(chan Event, 256)
	b.subs[id] = ch
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
	}
}

// Clear empties the buffer and notifies subscribers.
func (b *buffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = nil
	for _, ch := range b.subs {
		select {
		case ch <- Event{Clear: true}:
		default:
		}
	}
}
