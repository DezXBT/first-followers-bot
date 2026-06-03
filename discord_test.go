package main

import "testing"

func testBot() *Bot {
	return &Bot{config: &Config{FirstFollowersLimit: 20}}
}

func TestParseFirstArgs(t *testing.T) {
	b := testBot()
	cases := []struct {
		in         string
		wantClean  string
		wantLimit  int
	}{
		{"username", "username", 20},               // no number → default
		{"username 30", "username", 30},            // limit after handle
		{"30 username", "username", 30},            // limit before handle
		{"username 500", "username", 20},           // out-of-range → default, number stripped
		{"12345", "12345", 20},                     // numeric-only handle kept (not eaten)
		{"https://x.com/user/status/123", "https://x.com/user/status/123", 20}, // URL untouched
		{"user 0", "user", 20},                     // 0 out of range → default
		{"", "", 20},                               // empty
	}
	for _, c := range cases {
		clean, limit := b.parseFirstArgs(c.in)
		if clean != c.wantClean || limit != c.wantLimit {
			t.Errorf("parseFirstArgs(%q) = (%q, %d); want (%q, %d)", c.in, clean, limit, c.wantClean, c.wantLimit)
		}
	}
}

func TestMatchPrefix(t *testing.T) {
	cases := []struct {
		content string
		prefix  string
		wantRest string
		wantOK  bool
	}{
		{".first", ".first", "", true},
		{".first elonmusk", ".first", "elonmusk", true},
		{".first 30 elonmusk", ".first", "30 elonmusk", true},
		{".firstxyz", ".first", "", false},     // word boundary — must not match
		{".cek user", ".first", "", false},
		{".cek user", ".cek", "user", true},
	}
	for _, c := range cases {
		rest, ok := matchPrefix(c.content, c.prefix)
		if rest != c.wantRest || ok != c.wantOK {
			t.Errorf("matchPrefix(%q, %q) = (%q, %v); want (%q, %v)", c.content, c.prefix, rest, ok, c.wantRest, c.wantOK)
		}
	}
}

func TestNormalizeHandle(t *testing.T) {
	cases := map[string]string{
		"@elonmusk":                    "elonmusk",
		"https://x.com/elonmusk":       "elonmusk",
		"https://x.com/elonmusk?s=21":  "elonmusk",
		"https://twitter.com/elonmusk/": "elonmusk",
		"elonmusk":                     "elonmusk",
	}
	for in, want := range cases {
		if got := normalizeHandle(in); got != want {
			t.Errorf("normalizeHandle(%q) = %q; want %q", in, got, want)
		}
	}
}
