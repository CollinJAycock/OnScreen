package media

import "testing"

func TestReplayGainOnRescan(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	s := func(v string) *string { return &v }
	tags, analyzed := s(ReplayGainFromTags), s(ReplayGainAnalyzed)
	hashA, hashB := s("a"), s("b")

	cases := []struct {
		name      string
		existing  File
		p         CreateFileParams
		write     bool
		wantGain  *float64
		wantSrc   string
		wantClear bool
	}{
		{
			name:     "tags win over a measurement",
			existing: File{FileSize: 10, FileHash: hashA, ReplayGainTrackGain: f(-3), ReplayGainSource: analyzed},
			p:        CreateFileParams{FileSize: 10, FileHash: hashA, ReplayGainKnown: true, ReplayGainTrackGain: f(-7.5)},
			write:    true, wantGain: f(-7.5), wantSrc: ReplayGainFromTags,
		},
		{
			name:     "unchanged tags write nothing",
			existing: File{FileSize: 10, ReplayGainTrackGain: f(-7.5), ReplayGainSource: tags},
			p:        CreateFileParams{FileSize: 11, ReplayGainKnown: true, ReplayGainTrackGain: f(-7.5)},
			write:    false,
		},
		{
			name:     "removed tags clear the values",
			existing: File{FileSize: 10, ReplayGainTrackGain: f(-7.5), ReplayGainSource: tags},
			p:        CreateFileParams{FileSize: 11, ReplayGainKnown: true},
			write:    true, wantClear: true,
		},
		{
			name:     "an unread tag block keeps tag values",
			existing: File{FileSize: 10, ReplayGainTrackGain: f(-7.5), ReplayGainSource: tags},
			p:        CreateFileParams{FileSize: 11, ReplayGainKnown: false},
			write:    false,
		},
		{
			name:     "a measurement survives a scan of the same bytes",
			existing: File{FileSize: 10, FileHash: hashA, ReplayGainTrackGain: f(-3), ReplayGainSource: analyzed},
			p:        CreateFileParams{FileSize: 10, FileHash: hashA, ReplayGainKnown: true},
			write:    false,
		},
		{
			name:     "changed content drops the measurement",
			existing: File{FileSize: 10, FileHash: hashA, ReplayGainTrackGain: f(-3), ReplayGainSource: analyzed},
			p:        CreateFileParams{FileSize: 10, FileHash: hashB, ReplayGainKnown: true},
			write:    true, wantClear: true,
		},
		{
			name:     "changed size drops the measurement",
			existing: File{FileSize: 10, ReplayGainTrackGain: f(-3), ReplayGainSource: analyzed},
			p:        CreateFileParams{FileSize: 12},
			write:    true, wantClear: true,
		},
		{
			name:     "no values either side",
			existing: File{FileSize: 10},
			p:        CreateFileParams{FileSize: 12, ReplayGainKnown: true},
			write:    false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rg, write := ReplayGainOnRescan(c.existing, c.p)
			if write != c.write {
				t.Fatalf("write = %v, want %v", write, c.write)
			}
			if !write {
				return
			}
			if c.wantClear {
				if rg != (ReplayGain{}) {
					t.Errorf("want every value cleared, got %+v", rg)
				}
				return
			}
			if rg.TrackGain == nil || *rg.TrackGain != *c.wantGain || rgSource(rg.Source) != c.wantSrc {
				t.Errorf("got gain %v source %q", rg.TrackGain, rgSource(rg.Source))
			}
		})
	}
}
