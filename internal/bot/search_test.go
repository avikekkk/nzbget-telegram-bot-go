package bot

import "testing"

func TestParseSearchArgs(t *testing.T) {
	cases := []struct {
		in       string
		query    string
		sortMode string
		errText  string
	}{
		{in: "ubuntu", query: "ubuntu"},
		{in: "ubuntu 24.04 --mn", query: "ubuntu 24.04", sortMode: "min"},
		{in: "ubuntu --mx", query: "ubuntu", sortMode: "max"},
		{in: `"the matrix" 1999`, query: "the matrix 1999"},
		// A flag counts only as the last token.
		{in: "--mx ubuntu", errText: "Use --mx or --mn only at the end. Example: /search ubuntu --mn"},
		{in: "ubuntu --mx --mn", errText: "Use either --mx or --mn, not both."},
		{in: "ubuntu --mx --mx", errText: "Use --mx/--mn only once, at the end."},
	}

	for _, c := range cases {
		query, sortMode, errText := parseSearchArgs(c.in)
		if errText != c.errText {
			t.Errorf("parseSearchArgs(%q) error = %q, want %q", c.in, errText, c.errText)
			continue
		}
		if errText != "" {
			continue
		}
		if query != c.query || sortMode != c.sortMode {
			t.Errorf("parseSearchArgs(%q) = (%q, %q), want (%q, %q)",
				c.in, query, sortMode, c.query, c.sortMode)
		}
	}
}

func TestParseCommand(t *testing.T) {
	b := &Bot{username: "NZBGetBot"}

	cases := []struct {
		text    string
		command string
		args    string
		ok      bool
	}{
		{text: "/grab abc def", command: "grab", args: "abc def", ok: true},
		{text: "/Status", command: "status", ok: true},
		{text: "/grab@nzbgetbot xyz", command: "grab", args: "xyz", ok: true},
		// A list of IDs pastes as its own lines, and search results are one per
		// line, so the command word has to end at any whitespace -- not just at
		// a literal space, which used to swallow the whole list.
		{text: "/grab\nabc\ndef", command: "grab", args: "abc\ndef", ok: true},
		{text: "/grab@nzbgetbot\nabc", command: "grab", args: "abc", ok: true},
		{text: "/status\n", command: "status", ok: true},
		{text: "/grab\tabc", command: "grab", args: "abc", ok: true},
		// Addressed to another bot in a group: not ours to answer.
		{text: "/grab@someotherbot xyz"},
		{text: "not a command"},
		{text: "/"},
	}

	for _, c := range cases {
		command, args, ok := b.parseCommand(c.text)
		if ok != c.ok || command != c.command || args != c.args {
			t.Errorf("parseCommand(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.text, command, args, ok, c.command, c.args, c.ok)
		}
	}
}

// TestParseSearchArgsKeepsApostrophes is the regression test for a query that
// was split with shell rules. An apostrophe opened a quoted run that never
// closed, so it swallowed the rest of the line: the apostrophe vanished from
// the query and a trailing --mx went with it, leaving the results unsorted
// with nothing said about it.
func TestParseSearchArgsKeepsApostrophes(t *testing.T) {
	cases := []struct {
		in       string
		query    string
		sortMode string
	}{
		{in: "Marvel's Spider-Man 2", query: "Marvel's Spider-Man 2"},
		{in: "Marvel's Spider-Man 2 --mx", query: "Marvel's Spider-Man 2", sortMode: "max"},
		{in: "don't stop --mn", query: "don't stop", sortMode: "min"},
		// A quoted phrase still loses its wrapping quotes.
		{in: `"spider man" --mx`, query: "spider man", sortMode: "max"},
		{in: `it's "a test"`, query: "it's a test"},
	}

	for _, c := range cases {
		query, sortMode, parseErr := parseSearchArgs(c.in)
		if parseErr != "" {
			t.Errorf("parseSearchArgs(%q) refused a plain query: %s", c.in, parseErr)
			continue
		}
		if query != c.query || sortMode != c.sortMode {
			t.Errorf("parseSearchArgs(%q) = (%q, %q), want (%q, %q)",
				c.in, query, sortMode, c.query, c.sortMode)
		}
	}
}
