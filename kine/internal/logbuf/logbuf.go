// Package logbuf captures process log entries for the dashboard console
// viewer (/dashboard/console-log), the Go counterpart of 9router-go's
// internal/log console buffer.
//
// It is an io.Writer, so every log.Printf in the process lands here via
// log.SetOutput, and it also takes structured Emit calls that carry a level
// and a tag. Levels are stamped at emit time, never guessed from the text.
package logbuf

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// MaxLines matches CONSOLE_LOG_CONFIG.maxLines in the dashboard.
const MaxLines = 200

// Level mirrors the dashboard's four severities, ordered by verbosity.
type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

func (l Level) String() string {
	switch l {
	case Debug:
		return "debug"
	case Warn:
		return "warn"
	case Error:
		return "error"
	default:
		return "info"
	}
}

// ParseLevel accepts the names the dashboard sends.
func ParseLevel(s string) (Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return Debug, true
	case "info":
		return Info, true
	case "warn", "warning":
		return Warn, true
	case "error":
		return Error, true
	}
	return Info, false
}

// Entry is one captured line plus the metadata known at emit time.
type Entry struct {
	Time  string `json:"time"`
	Level string `json:"level"`
	Tag   string `json:"tag,omitempty"`
	Line  string `json:"line"`
}

// Event is one message on the console feed: a new entry or a clear notice.
type Event struct {
	Entry Entry
	Clear bool
}

type buffer struct {
	mu      sync.Mutex
	entries []Entry
	subs    map[int]chan Event
	nextID  int
	level   Level
	partial []byte
}

var global = &buffer{subs: map[int]chan Event{}, level: Info}

// init honours LOG_LEVEL for the capture floor, the same variable 9router-go
// reads, so a restart can start quieter without touching the dashboard.
func init() {
	if l, ok := ParseLevel(os.Getenv("LOG_LEVEL")); ok {
		global.level = l
	}
}

// ansiSGR matches terminal colour sequences, which would otherwise reach the
// browser as garbage glyphs.
var ansiSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	return ansiSGR.ReplaceAllString(s, "")
}

// Default returns the process-wide buffer.
func Default() *buffer { return global }

// Emit appends one structured entry when it passes the active level filter.
func (b *buffer) Emit(level Level, tag, format string, args ...any) {
	b.append(Entry{
		Time:  time.Now().Format("15:04:05.000"),
		Level: level.String(),
		Tag:   tag,
		Line:  stripANSI(fmt.Sprintf(format, args...)),
	})
}

// Write implements io.Writer for log.Printf output. Those lines carry no
// level or tag of their own, so they are recorded as info and split on a
// leading "TAG:" the caller may have written.
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
		if strings.TrimSpace(line) == "" {
			continue
		}
		// log.Ltime already stamped this line; the row carries its own clock,
		// so drop the duplicate prefix.
		if len(line) > 9 && line[2] == ':' && line[5] == ':' && line[8] == ' ' {
			line = line[9:]
		}
		b.push(Entry{Time: time.Now().Format("15:04:05.000"), Level: Info.String(), Line: stripANSI(line)})
	}
	return len(p), nil
}

func (b *buffer) append(e Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.push(e)
}

// push appends under the caller's lock.
func (b *buffer) push(e Entry) {
	if levelOf(e.Level) < b.level {
		return
	}
	b.entries = append(b.entries, e)
	if len(b.entries) > MaxLines {
		b.entries = append([]Entry(nil), b.entries[len(b.entries)-MaxLines:]...)
	}
	for _, ch := range b.subs {
		select {
		case ch <- Event{Entry: e}:
		default: // slow subscriber: drop rather than block logging
		}
	}
}

func levelOf(name string) Level {
	if l, ok := ParseLevel(name); ok {
		return l
	}
	return Info
}

// Entries returns a copy of the buffered entries (never nil, so the dashboard
// always receives an array).
func (b *buffer) Entries() []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append(make([]Entry, 0, len(b.entries)), b.entries...)
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
	b.entries = nil
	for _, ch := range b.subs {
		select {
		case ch <- Event{Clear: true}:
		default:
		}
	}
}

// Level returns the active capture level.
func (b *buffer) Level() Level {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.level
}

// SetLevel drops entries below l before they are buffered.
func (b *buffer) SetLevel(l Level) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.level = l
}
