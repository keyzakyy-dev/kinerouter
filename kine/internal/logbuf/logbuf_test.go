package logbuf

import (
	"io"
	"strings"
	"testing"
)

// Lines are captured, the ring is capped at MaxLines, and a subscriber sees
// the same entry the process logged.
func TestCaptureRingAndSubscribe(t *testing.T) {
	b := Default()
	b.Clear()
	b.SetLevel(Debug)

	ch, cancel := b.Subscribe()
	defer cancel()

	io.WriteString(b, "12:00:00 first\n")
	b.Emit(Warn, "request", "second line")

	got := b.Entries()
	if len(got) != 2 || got[0].Line != "first" || got[0].Level != "info" {
		t.Fatalf("entries: %#v", got)
	}
	if got[1].Level != "warn" || got[1].Tag != "request" || got[1].Time == "" {
		t.Fatalf("structured entry: %#v", got[1])
	}

	ev := <-ch
	if ev.Entry.Line != "first" {
		t.Fatalf("event: %#v", ev)
	}

	for i := 0; i < MaxLines+50; i++ {
		b.Emit(Info, "", "x")
	}
	if n := len(b.Entries()); n != MaxLines {
		t.Fatalf("ring size %d, want %d", n, MaxLines)
	}

	b.Clear()
	if n := len(b.Entries()); n != 0 {
		t.Fatalf("clear left %d lines", n)
	}
	// Drain queued line events, then expect the clear notice.
	for {
		select {
		case ev := <-ch:
			if ev.Clear {
				return
			}
		default:
			t.Fatal("no clear event")
		}
	}
}

// The level filter drops verbosity below the active level at emit time.
func TestLevelFilter(t *testing.T) {
	b := Default()
	b.Clear()
	b.SetLevel(Warn)
	defer b.SetLevel(Info)

	b.Emit(Debug, "", "hidden")
	b.Emit(Info, "", "hidden too")
	b.Emit(Error, "", "kept")

	got := b.Entries()
	if len(got) != 1 || got[0].Line != "kept" {
		t.Fatalf("entries: %#v", got)
	}
	if b.Level() != Warn {
		t.Fatalf("level = %v", b.Level())
	}
}

// A trailing partial log line is held until its newline arrives.
func TestPartialLineHeld(t *testing.T) {
	b := Default()
	b.Clear()
	b.SetLevel(Debug)

	io.WriteString(b, "no newline yet")
	if n := len(b.Entries()); n != 0 {
		t.Fatalf("partial line leaked: %v", b.Entries())
	}
	io.WriteString(b, " and done\n")
	if got := b.Entries(); len(got) != 1 || !strings.HasSuffix(got[0].Line, "and done") {
		t.Fatalf("entries: %#v", got)
	}
}

// Terminal colour escapes must not reach the browser, whichever path captured
// the line.
func TestStripANSI(t *testing.T) {
	b := Default()
	b.Clear()
	b.SetLevel(Debug)
	defer b.SetLevel(Info)

	b.Emit(Info, "tag", "\x1b[32mgreen\x1b[0m")
	io.WriteString(b, "\x1b[31mraw red\x1b[0m\n")

	got := b.Entries()
	if len(got) != 2 || got[0].Line != "green" || got[1].Line != "raw red" {
		t.Fatalf("entries: %#v", got)
	}
}
