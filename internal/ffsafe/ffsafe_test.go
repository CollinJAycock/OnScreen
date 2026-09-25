package ffsafe

import (
	"strings"
	"testing"
)

func TestInputProtocolArgs_LocalDeniesNetwork(t *testing.T) {
	for _, in := range []string{
		"/media/Movies/x.mkv",
		`C:\media\x.mkv`,
		"pipe:0",
		"relative/path.mkv",
		"ftp://example/x", // non-http scheme: treated as local, network denied
	} {
		args := InputProtocolArgs(in)
		if len(args) != 2 || args[0] != "-protocol_whitelist" {
			t.Fatalf("%q: unexpected args %v", in, args)
		}
		for _, banned := range []string{"http", "https", "tcp", "tls"} {
			for _, p := range strings.Split(args[1], ",") {
				if p == banned {
					t.Errorf("%q: local input must not permit %q (got %q)", in, banned, args[1])
				}
			}
		}
		if !strings.Contains(args[1], "file") {
			t.Errorf("%q: must permit file (got %q)", in, args[1])
		}
	}
}

func TestInputProtocolArgs_HTTPAllowsNetwork(t *testing.T) {
	for _, in := range []string{
		"http://10.0.0.5:7070/media/stream/x?token=y",
		"https://cdn.example/x.mkv",
	} {
		args := InputProtocolArgs(in)
		if args[0] != "-protocol_whitelist" {
			t.Fatalf("%q: unexpected args %v", in, args)
		}
		set := map[string]bool{}
		for _, p := range strings.Split(args[1], ",") {
			set[p] = true
		}
		for _, need := range []string{"file", "http", "https", "tcp", "tls"} {
			if !set[need] {
				t.Errorf("%q: http source must permit %q (got %q)", in, need, args[1])
			}
		}
	}
}

func TestWhitelist_MatchesArgs(t *testing.T) {
	for _, in := range []string{"/x.mkv", "http://h/x", "pipe:0"} {
		if got, want := Whitelist(in), InputProtocolArgs(in)[1]; got != want {
			t.Errorf("%q: Whitelist=%q but InputProtocolArgs=%q", in, got, want)
		}
	}
}
