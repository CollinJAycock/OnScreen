package ffsafe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConcatInputArgs_LocalListDeniesNetwork(t *testing.T) {
	args := ConcatInputArgs([]ConcatPart{{Input: "/m/a cd1.mkv"}, {Input: `C:\m\a cd2.mkv`}, {Input: `\\nas\m\a cd3.mkv`}})
	if strings.Join(args[:4], " ") != "-f concat -safe 0" || args[4] != "-protocol_whitelist" {
		t.Fatalf("args = %v", args)
	}
	for _, p := range strings.Split(args[5], ",") {
		switch p {
		case "http", "https", "tcp", "tls":
			t.Fatalf("local concat list must not permit %q (got %q)", p, args[5])
		}
	}
}

func TestConcatInputArgs_URLListAllowsNetwork(t *testing.T) {
	args := ConcatInputArgs([]ConcatPart{
		{Input: "http://10.0.0.5:7070/media/stream/a?token=x"},
		{Input: "http://10.0.0.5:7070/media/stream/b?token=y"},
	})
	if args[5] != networkProtocols {
		t.Fatalf("whitelist = %q, want %q", args[5], networkProtocols)
	}
}

func TestConcatParts_Refused(t *testing.T) {
	cases := map[string][]ConcatPart{
		"empty":            nil,
		"blank entry":      {{Input: "/a.mkv"}, {Input: ""}},
		"newline":          {{Input: "/a.mkv\nfile /etc/passwd"}},
		"carriage return":  {{Input: "/a.mkv\r"}},
		"relative path":    {{Input: "a.mkv"}},
		"other protocol":   {{Input: "subfile:,,start,0,end,0,,:/a.mkv"}},
		"data uri":         {{Input: "data:video/mp4;base64,AAAA"}},
		"ftp":              {{Input: "ftp://h/a.mkv"}},
		"mixed local, url": {{Input: "/a.mkv"}, {Input: "http://h/b"}},
		"mixed url, local": {{Input: "https://h/a"}, {Input: "/b.mkv"}},
	}
	for name, parts := range cases {
		// A refused list still gets args, but in safe mode under the local
		// allowlist: ffmpeg then refuses to open it.
		args := strings.Join(ConcatInputArgs(parts), " ")
		if strings.Contains(args, "-safe 0") || strings.Contains(args, "http") {
			t.Errorf("%s: ConcatInputArgs = %q, want safe mode without network", name, args)
		}
		if err := WriteConcatList(filepath.Join(t.TempDir(), "l.ffconcat"), parts); err == nil {
			t.Errorf("%s: WriteConcatList accepted %v", name, parts)
		}
	}
}

func TestWriteConcatList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parts.ffconcat")
	err := WriteConcatList(path, []ConcatPart{
		{Input: "/m/Movie (2001)/Movie (2001) cd1.mkv", DurationSec: 3000.5},
		{Input: "/m/It's (2001)/It's cd2.mkv"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "ffconcat version 1.0\n" +
		"file '/m/Movie (2001)/Movie (2001) cd1.mkv'\n" +
		"duration 3000.500\n" +
		`file '/m/It'\''s (2001)/It'\''s cd2.mkv'` + "\n"
	if string(got) != want {
		t.Fatalf("list =\n%s\nwant\n%s", got, want)
	}
}

func TestWriteConcatList_InOutPoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cut.ffconcat")
	err := WriteConcatList(path, []ConcatPart{
		{Input: "/dvr/Show.mkv", OutPointSec: 600.25},
		{Input: "/dvr/Show.mkv", InPointSec: 780, OutPointSec: 1500.5},
		{Input: "/dvr/Show.mkv", InPointSec: 1680},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "ffconcat version 1.0\n" +
		"file '/dvr/Show.mkv'\noutpoint 600.250\n" +
		"file '/dvr/Show.mkv'\ninpoint 780.000\noutpoint 1500.500\n" +
		"file '/dvr/Show.mkv'\ninpoint 1680.000\n"
	if string(got) != want {
		t.Fatalf("list =\n%s\nwant\n%s", got, want)
	}
}
