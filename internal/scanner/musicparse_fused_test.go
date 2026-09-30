package scanner

import (
	"fmt"
	"path/filepath"
	"testing"
)

// fusedNames names tracks 1..n of disc d the way a fused set does:
// fmt.Sprintf(format, d*100+t, t) for each track t.
func fusedNames(format string, d, n int) []string {
	var out []string
	for t := 1; t <= n; t++ {
		out = append(out, fmt.Sprintf(format, d*100+t, t))
	}
	return out
}

// countdown names ranks 1..n of a folder numbered straight through,
// "001 - Artist 1 - Title.flac" onward.
func countdown(n int) []string {
	var out []string
	for r := 1; r <= n; r++ {
		out = append(out, fmt.Sprintf("%03d - Artist %d - Title.flac", r, r))
	}
	return out
}

// Some multi-disc releases fuse the disc onto the track number in the file
// name ("112-the_beatles-piggies-repack.flac": disc 1, track 12) and carry no
// DISCNUMBER tag. The name reads as track 112 on its own; the tags' track
// number shows where the disc ends, and the folder shows the set: this
// disc's tracks before it and another disc's first track, named with the
// same separator.
func TestReadMusicTags_FusedDiscTrackName(t *testing.T) {
	beatles := "%03d-the_beatles-song_%d.flac"
	set := append(fusedNames(beatles, 1, 17), fusedNames(beatles, 2, 15)...)
	for _, tc := range []struct {
		name      string
		siblings  []string // other files in the folder
		comments  []string
		wantDisc  int
		wantTrack int
	}{
		{name: "112-the_beatles-piggies-repack.flac", siblings: set, comments: []string{"TRACKNUMBER=12"}, wantDisc: 1, wantTrack: 12},
		{name: "212-the_beatles-revolution_9-repack.flac", siblings: set, comments: []string{"TRACKNUMBER=12"}, wantDisc: 2, wantTrack: 12},
		{name: "216-the_beatles-bonus.flac", siblings: set, comments: []string{"TRACKNUMBER=16"}, wantDisc: 2, wantTrack: 16},
		// A compilation: each name opens with its own track's artist.
		{name: "112-cassius-1999.flac", siblings: append(fusedNames("%03d-artist_%d-song.flac", 1, 11), "201-moby-porcelain.flac"), comments: []string{"TRACKNUMBER=12"}, wantDisc: 1, wantTrack: 12},
		// Titles first.
		{name: "212 - Revolution 9.flac", siblings: append(fusedNames("%03d - Title %d.flac", 2, 11), "101 - Back in the USSR.flac"), comments: []string{"TRACKNUMBER=12"}, wantDisc: 2, wantTrack: 12},
		{name: "501 Five.flac", siblings: []string{"101 One.flac"}, comments: []string{"TRACKNUMBER=1"}, wantDisc: 5, wantTrack: 1},
		{name: "305.Five.flac", siblings: []string{"101.One.flac", "301.One.flac", "302.Two.flac"}, comments: []string{"TRACKNUMBER=5"}, wantDisc: 3, wantTrack: 5},
		// A gap in the disc's run (a missing track) is tolerated.
		{name: "112-gap-song.flac", siblings: append([]string{"101-gap-a.flac", "103-gap-c.flac", "105-gap-e.flac", "107-gap-g.flac", "109-gap-i.flac", "111-gap-k.flac"}, "201-gap-b.flac"), comments: []string{"TRACKNUMBER=12"}, wantDisc: 1, wantTrack: 12},
		// The folder holds no set: a lone fused-looking name isn't one.
		{name: "112-the_beatles-alone.flac", comments: []string{"TRACKNUMBER=12"}, wantDisc: 0, wantTrack: 12},
		// This disc's run, but no other disc.
		{name: "112-the_beatles-one_disc.flac", siblings: fusedNames(beatles, 1, 17), comments: []string{"TRACKNUMBER=12"}, wantDisc: 0, wantTrack: 12},
		// No run of this disc's tracks before it.
		{name: "312-the_beatles-third.flac", siblings: append([]string{"301-the_beatles-a.flac"}, set...), comments: []string{"TRACKNUMBER=12"}, wantDisc: 0, wantTrack: 12},
		// A band named with a number: every name opens with it, and one
		// track per album has the matching number.
		{name: "311 - 11 - Prisoner.flac", siblings: []string{"311 - 01 - Opener.flac", "311 - 02 - Second.flac"}, comments: []string{"TRACKNUMBER=11"}, wantDisc: 0, wantTrack: 11},
		{name: "702 - Where My Girls At.flac", siblings: []string{"702 - Get It Together.flac"}, comments: []string{"TRACKNUMBER=2"}, wantDisc: 0, wantTrack: 2},
		{name: "808 State - Ex-El - 08 Lift.flac", siblings: []string{"808 State - Ex-El - 01 San Francisco.flac"}, comments: []string{"TRACKNUMBER=8"}, wantDisc: 0, wantTrack: 8},
		// Stray numbered files loose in a folder beside a real set: named
		// with another separator, or with no run of their disc behind them.
		{name: "311 - 11 - Prisoner (flat).flac", siblings: set, comments: []string{"TRACKNUMBER=11"}, wantDisc: 0, wantTrack: 11},
		{name: "212 (Live).flac", siblings: set, comments: []string{"TRACKNUMBER=12"}, wantDisc: 0, wantTrack: 12},
		{name: "112 - Cupid.flac", siblings: set, comments: []string{"TRACKNUMBER=12"}, wantDisc: 0, wantTrack: 12},
		{name: "311-stray-song.flac", siblings: set, comments: []string{"TRACKNUMBER=11"}, wantDisc: 0, wantTrack: 11},
		// A folder numbered straight through, not by disc: a countdown whose
		// files keep their albums' tags. It holds 100, 200 and 001-099.
		{name: "301 - Nirvana - Smells Like Teen Spirit.flac", siblings: countdown(350), comments: []string{"TRACKNUMBER=1"}, wantDisc: 0, wantTrack: 1},
		{name: "212 - Artist - Hit.flac", siblings: append(append(fusedNames("%03d - Artist %d - Title.flac", 1, 99), "100 - Artist - Hundred.flac", "200 - Artist - Two Hundred.flac"), fusedNames("%03d - Artist %d - Title.flac", 2, 11)...), comments: []string{"TRACKNUMBER=12"}, wantDisc: 0, wantTrack: 12},
		// A disc tag wins over the name.
		{name: "112-the_beatles-tagged.flac", siblings: set, comments: []string{"TRACKNUMBER=12", "DISCNUMBER=3"}, wantDisc: 3, wantTrack: 12},
		// A real track 112 of a long album is tagged 112: no disc.
		{name: "112 - Hundred Twelve.flac", siblings: set, comments: []string{"TRACKNUMBER=112"}, wantDisc: 0, wantTrack: 112},
		// The prefix doesn't end in the tagged track: not a fused name.
		{name: "112-the_beatles-mismatch.flac", siblings: set, comments: []string{"TRACKNUMBER=13"}, wantDisc: 0, wantTrack: 13},
		// A title that is a number, with no separator after it.
		{name: "1901.flac", siblings: set, comments: []string{"TRACKNUMBER=1"}, wantDisc: 0, wantTrack: 1},
		{name: "101.flac", siblings: []string{"201.flac"}, comments: []string{"TRACKNUMBER=1"}, wantDisc: 0, wantTrack: 1},
		// A leading zero is no disc.
		{name: "012-the_beatles-zero.flac", siblings: set, comments: []string{"TRACKNUMBER=12"}, wantDisc: 0, wantTrack: 12},
		// No track tag: the name's number is all there is, and it is 112.
		{name: "112-the_beatles-untagged.flac", siblings: set, comments: nil, wantDisc: 0, wantTrack: 112},
		// The usual "D-TT" form still reads as before.
		{name: "2-12 Hyphenated.flac", comments: []string{"TRACKNUMBER=12"}, wantDisc: 2, wantTrack: 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "Artist", "Box")
			for _, s := range tc.siblings {
				writeTaggedFLAC(t, filepath.Join(dir, s), "TITLE=Sibling", "ARTIST=Artist", "ALBUM=Box", "TRACKNUMBER=1")
			}
			path := filepath.Join(dir, tc.name)
			writeTaggedFLAC(t, path, append([]string{"TITLE=Song", "ARTIST=Artist", "ALBUM=Box"}, tc.comments...)...)
			tags, err := ReadMusicTags(path)
			if err != nil {
				t.Fatalf("ReadMusicTags: %v", err)
			}
			if tags.Disc != tc.wantDisc || tags.Track != tc.wantTrack {
				t.Errorf("disc %d track %d, want disc %d track %d", tags.Disc, tags.Track, tc.wantDisc, tc.wantTrack)
			}
		})
	}
}
