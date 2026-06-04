package main

import "testing"

func testBot() *Bot {
	return &Bot{config: &Config{FirstFollowersLimit: 20}}
}

func TestParseFirstArgs(t *testing.T) {
	b := testBot()
	cases := []struct {
		in        string
		wantClean string
		wantLimit int
	}{
		{"username", "username", 20},                                           // no number → default
		{"username 30", "username", 30},                                        // limit after handle
		{"30 username", "username", 30},                                        // limit before handle
		{"username 500", "username", 20},                                       // out-of-range → default, number stripped
		{"12345", "12345", 20},                                                 // numeric-only handle kept (not eaten)
		{"https://x.com/user/status/123", "https://x.com/user/status/123", 20}, // URL untouched
		{"user 0", "user", 20},                                                 // 0 out of range → default
		{"", "", 20},                                                           // empty
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
		content  string
		prefix   string
		wantRest string
		wantOK   bool
	}{
		{".first", ".first", "", true},
		{".first elonmusk", ".first", "elonmusk", true},
		{".first 30 elonmusk", ".first", "30 elonmusk", true},
		{".firstxyz", ".first", "", false}, // word boundary — must not match
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

func TestLinkifyMentions(t *testing.T) {
	cases := map[string]string{
		"gm @elonmusk follow":   "gm [@elonmusk](https://x.com/elonmusk) follow",
		"@jack at start":        "[@jack](https://x.com/jack) at start",
		"no mentions here":      "no mentions here",
		"email me@gmail.com ok": "email me@gmail.com ok", // not preceded by boundary → not linkified
		"two @a and @b":         "two [@a](https://x.com/a) and [@b](https://x.com/b)",
	}
	for in, want := range cases {
		if got := linkifyMentions(in); got != want {
			t.Errorf("linkifyMentions(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestParseFlexTime(t *testing.T) {
	// The bio timestamp format that previously rendered raw.
	shouldParse := []string{
		"2026-03-19T17:02:09.904115+00:00",
		"2026-04-08T06:49:07.461474+00:00",
		"2006-01-02T15:04:05Z",
		"Sat May 01 12:55:14 +0000 2021",
	}
	for _, s := range shouldParse {
		if _, ok := parseFlexTime(s); !ok {
			t.Errorf("parseFlexTime(%q) failed to parse", s)
		}
	}
	if _, ok := parseFlexTime("not a date"); ok {
		t.Errorf("parseFlexTime(\"not a date\") should fail")
	}
}

func TestNormalizeHandle(t *testing.T) {
	cases := map[string]string{
		"@elonmusk":                     "elonmusk",
		"https://x.com/elonmusk":        "elonmusk",
		"https://x.com/elonmusk?s=21":   "elonmusk",
		"https://twitter.com/elonmusk/": "elonmusk",
		"elonmusk":                      "elonmusk",
	}
	for in, want := range cases {
		if got := normalizeHandle(in); got != want {
			t.Errorf("normalizeHandle(%q) = %q; want %q", in, got, want)
		}
	}
}
