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
