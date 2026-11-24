package main

import (
	"reflect"
	"testing"
)

// The login entry deployed by autostart.EnableInstall runs the binary with
// "--tray --skip-update". Both must be stripped before subcommand dispatch, or
// runCLI answers "unknown flag" and the gateway never starts at login.
func TestParseServerFlags(t *testing.T) {
	o, rest := parseServerFlags([]string{"--tray", "--skip-update"})
	if !o.tray || !o.skipUpdate || len(rest) != 0 {
		t.Fatalf("tray=%v skip=%v rest=%v", o.tray, o.skipUpdate, rest)
	}

	// Order independence: the entry is free to put --skip-update first.
	o, rest = parseServerFlags([]string{"--skip-update", "--tray"})
	if !o.tray || !o.skipUpdate || len(rest) != 0 {
		t.Fatalf("reordered: %#v rest=%v", o, rest)
	}
}

// KeyRouter's cli.js accepts -p/--port and -H/--host. Without them an autostart
// line copied from KeyRouter (or written by hand) dies on "unknown flag".
func TestParsePortHostFlags(t *testing.T) {
	for _, argv := range [][]string{
		{"--port", "20129", "--host", "127.0.0.1"},
		{"-p", "20129", "-H", "127.0.0.1"},
	} {
		o, rest := parseServerFlags(argv)
		if o.port != 20129 || o.host != "127.0.0.1" {
			t.Fatalf("%v parsed as %#v", argv, o)
		}
		if len(rest) != 0 {
			t.Fatalf("%v left %#v", argv, rest)
		}
	}

	// Combined with the tray flags, as a hand-written login entry might.
	o, rest := parseServerFlags([]string{"--tray", "--skip-update", "-p", "20200"})
	if !o.tray || !o.skipUpdate || o.port != 20200 || len(rest) != 0 {
		t.Fatalf("combined: %#v rest=%v", o, rest)
	}

	// A missing value must not consume the next flag or panic.
	o, _ = parseServerFlags([]string{"--port"})
	if o.port != 0 {
		t.Fatalf("dangling --port produced %d", o.port)
	}
	o, _ = parseServerFlags([]string{"--port", "abc"})
	if o.port != 0 {
		t.Fatalf("non-numeric port produced %d", o.port)
	}
}

// connect and show have their own flags (--tools, --password, --model, -p is not
// theirs). Their argv must reach them untouched.
func TestSubcommandArgsPassThrough(t *testing.T) {
	argv := []string{"connect", "http://localhost:20128", "--tools", "all", "--password", "x"}
	o, rest := parseServerFlags(argv)
	if o.tray || o.skipUpdate || o.port != 0 || o.host != "" {
		t.Fatalf("subcommand consumed flags: %#v", o)
	}
	if !reflect.DeepEqual(rest, argv) {
		t.Fatalf("rest = %#v", rest)
	}

	// Same for show, which takes positional tool names.
	argv = []string{"show", "claude", "--json"}
	if _, rest := parseServerFlags(argv); !reflect.DeepEqual(rest, argv) {
		t.Fatalf("show rest = %#v", rest)
	}
}
