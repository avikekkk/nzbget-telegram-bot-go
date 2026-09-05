package bot

import "testing"

func TestURLReferencesID(t *testing.T) {
	cases := []struct {
		url, id string
		want    bool
	}{
		{"https://hydra/getnzb/api/1234?apikey=k", "1234", true},
		{"https://hydra/getnzb/api/1234?apikey=k", "123", false},
		{"https://hydra/api?t=get&id=abc123&apikey=k", "abc123", true},
		{"https://hydra/api?t=get&id=abc1234&apikey=k", "abc123", false},
		{"https://hydra/api?t=get&id=abc123", "", false},
		{"", "abc123", false},
	}
	for _, tc := range cases {
		if got := urlReferencesID(tc.url, tc.id); got != tc.want {
			t.Errorf("urlReferencesID(%q, %q) = %v, want %v", tc.url, tc.id, got, tc.want)
		}
	}
}
