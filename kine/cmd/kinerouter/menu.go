package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"
)

// chooseMenu is the arrow-key selector 9Router shows for its interface menu
// (cli/src/cli/utils/input.js selectMenu): ↑/↓ move, Enter picks, Esc cancels,
// the selected row is highlighted. Digits and j/k are accepted too, so a
// keypad-only terminal still works. Returns -1 when the user cancels.
func chooseMenu(title, subtitle string, items []string) (int, error) {
	fd := int(os.Stdin.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return -1, err
	}
	defer term.Restore(fd, state)

	sel := 0
	// Raw mode turns off output post-processing, so every line ends \r\n;
	// with a bare \n the menu walks down the screen as a staircase.
	width := 40
	if w, _, err := term.GetSize(fd); err == nil && w > 0 && w < width {
		width = w
	}
	const (
		reset      = "\x1b[0m"
		bright     = "\x1b[1m"
		dim        = "\x1b[2m"
		reverse    = "\x1b[7m"
		terracotta = "\x1b[38;2;217;119;87m"
	)
	rule := terracotta + strings.Repeat("=", width) + reset
	draw := func() {
		var b strings.Builder
		b.WriteString("\x1b[2J\x1b[H")
		b.WriteString("\r\n" + rule + "\r\n")
		b.WriteString("  " + bright + terracotta + title + reset + "\r\n")
		if subtitle != "" {
			b.WriteString("  " + dim + subtitle + reset + "\r\n")
		}
		b.WriteString(rule + "\r\n\r\n")
		win := runtime.GOOS == "windows"
		for i, it := range items {
			icon := "☆"
			if win {
				icon = " "
			}
			if i == sel {
				icon = "★"
				if win {
					icon = ">"
				}
				b.WriteString(" " + reverse + bright + icon + " " + it + reset + "\r\n")
			} else {
				b.WriteString("  " + icon + " " + it + "\r\n")
			}
		}
		fmt.Print(b.String())
	}

	draw()
	r := bufio.NewReader(os.Stdin)
	for {
		key, err := readKey(r)
		if err != nil {
			return -1, err
		}
		switch key {
		case "up", "k":
			sel = (sel - 1 + len(items)) % len(items)
		case "down", "j":
			sel = (sel + 1) % len(items)
		case "enter":
			return sel, nil
		case "esc", "q":
			return -1, nil
		default:
			if n := parseMenuDigit(key); n >= 1 && n <= len(items) {
				return n - 1, nil
			}
			continue
		}
		draw()
	}
}

func parseMenuDigit(k string) int {
	if len(k) != 1 || k[0] < '1' || k[0] > '9' {
		return 0
	}
	return int(k[0] - '0')
}

// readKey decodes one keypress. Arrow keys arrive as ESC [ A/B in a single
// write, so the two trailing bytes are already buffered when ESC is read.
func readKey(r *bufio.Reader) (string, error) {
	b, err := r.ReadByte()
	if err != nil {
		return "", err
	}
	switch b {
	case 0x1b:
		if r.Buffered() == 0 {
			// Some terminals split the sequence; give them a moment.
			deadline := time.Now().Add(30 * time.Millisecond)
			for r.Buffered() < 2 && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
		}
		if r.Buffered() >= 2 {
			seq := make([]byte, 2)
			if _, err := io.ReadFull(r, seq); err == nil && seq[0] == '[' {
				switch seq[1] {
				case 'A':
					return "up", nil
				case 'B':
					return "down", nil
				}
			}
		}
		return "esc", nil
	case '\r', '\n':
		return "enter", nil
	case 3:
		return "esc", nil // Ctrl+C
	}
	return string(b), nil
}
