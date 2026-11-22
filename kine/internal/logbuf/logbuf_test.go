package logbuf

import (
	"io"
	"strings"
	"testing"
)

// Lines are captured, the ring is capped at MaxLines, and a subscriber sees
// the same text the process logged.
func TestCaptureRingAndSubscribe(t *testing.T) {
	b := Default()
	b.Clear()

	ch, cancel := b.Subscribe()
	defer cancel()

	io.WriteString(b, "12:00:00 first\n")
	io.WriteString(b, "second line\npartial")

	if got := b.Lines(); len(got) != 2 || got[0] != "12:00:00 first" {
		t.Fatalf("lines: %#v", got)
	}

	got := <-ch
	if got.Line != "12:00:00 first" {
		t.Fatalf("event: %#v", got)
	}

	for i := 0; i < MaxLines+50; i++ {
		io.WriteString(b, "x\n")
	}
	if n := len(b.Lines()); n != MaxLines {
		t.Fatalf("ring size %d, want %d", n, MaxLines)
	}

	b.Clear()
	if n := len(b.Lines()); n != 0 {
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

// ANSI escapes from coloured loggers would bleed into the browser.
func TestStripAnsiNotNeededButPartialHeld(t *testing.T) {
	b := Default()
	b.Clear()
	io.WriteString(b, "no newline yet")
	if n := len(b.Lines()); n != 0 {
		t.Fatalf("partial line leaked: %v", b.Lines())
	}
	io.WriteString(b, " and done\n")
	if got := b.Lines(); len(got) != 1 || !strings.HasSuffix(got[0], "and done") {
		t.Fatalf("lines: %#v", got)
	}
}
