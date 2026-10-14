package scanner

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dhowden/tag"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

func TestSplitFeatured(t *testing.T) {
	cases := []struct{ in, primary, clause string }{
		{"Jay-Z feat. Rihanna", "Jay-Z", "Rihanna"},
		{"Daft Punk ft. Pharrell Williams & Nile Rodgers", "Daft Punk", "Pharrell Williams & Nile Rodgers"},
		{"Mark Ronson featuring Bruno Mars", "Mark Ronson", "Bruno Mars"},
		{"Umbrella (feat. Jay-Z)", "Umbrella", "Jay-Z"},
		{"Song [ft. A, B] (Remix)", "Song (Remix)", "A, B"},
		// The primary is never split.
		{"Earth, Wind & Fire", "Earth, Wind & Fire", ""},
		{"Simon & Garfunkel", "Simon & Garfunkel", ""},
		{"Nick Cave and the Bad Seeds", "Nick Cave and the Bad Seeds", ""},
		// "feat"/"ft" need their dot: a band called Little Feat stays put.
		{"Little Feat", "Little Feat", ""},
		{"Little Feat feat. Bonnie Raitt", "Little Feat", "Bonnie Raitt"},
	}
	for _, c := range cases {
		p, cl := splitFeatured(c.in)
		if p != c.primary || cl != c.clause {
			t.Errorf("splitFeatured(%q) = %q, %q; want %q, %q", c.in, p, cl, c.primary, c.clause)
		}
	}
}

func TestSplitFeaturedNames(t *testing.T) {
	known := func(names ...string) func(string) bool {
		set := map[string]bool{}
		for _, n := range names {
			set[strings.ToLower(n)] = true
		}
		return func(s string) bool { return set[strings.ToLower(s)] }
	}
	cases := []struct {
		clause string
		known  func(string) bool
		want   []string
	}{
		{"Rihanna", nil, []string{"Rihanna"}},
		{"Pharrell Williams & Nile Rodgers", nil, []string{"Pharrell Williams", "Nile Rodgers"}},
		{"A, B and C", nil, []string{"A", "B", "C"}},
		// A known band inside the clause stays whole, alone or in a list.
		{"Earth, Wind & Fire", known("Earth, Wind & Fire"), []string{"Earth, Wind & Fire"}},
		{"X & Earth, Wind & Fire", known("Earth, Wind & Fire"), []string{"X", "Earth, Wind & Fire"}},
		{"Florence and the Machine, Y", known("Florence and the Machine"), []string{"Florence and the Machine", "Y"}},
		{"", nil, nil},
	}
	for _, c := range cases {
		if got := splitFeaturedNames(c.clause, c.known); !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitFeaturedNames(%q) = %q, want %q", c.clause, got, c.want)
		}
	}
}

func TestTrackCredits(t *testing.T) {
	jay, riri := uuid.New(), uuid.New()

	// Fallback: the artist tag's primary plus its feat. clause, and the
	// title's bracketed guest.
	got := trackCredits(&MusicTags{Artist: "Jay-Z feat. Rihanna", Title: "Run This Town (feat. Kanye West)"}, nil)
	want := []ArtistCredit{
		{Name: "Jay-Z"},
		{Name: "Rihanna", Featured: true},
		{Name: "Kanye West", Featured: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fallback credits = %+v", got)
	}

	// ARTISTS is preferred and pairs with the artist MBIDs by position;
	// the feat. clause says which of them are featured.
	got = trackCredits(&MusicTags{
		Artist:      "Jay-Z feat. Rihanna",
		Artists:     []string{"Jay-Z", "Rihanna"},
		ArtistMBIDs: []uuid.UUID{jay, riri},
	}, nil)
	want = []ArtistCredit{{Name: "Jay-Z", MBID: jay}, {Name: "Rihanna", MBID: riri, Featured: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ARTISTS credits = %+v", got)
	}

	// Mismatched MBID count: no pairing at all rather than a wrong one.
	got = trackCredits(&MusicTags{Artists: []string{"A", "B"}, ArtistMBIDs: []uuid.UUID{jay}}, nil)
	for _, c := range got {
		if c.MBID != uuid.Nil {
			t.Errorf("unpaired MBID used: %+v", got)
		}
	}

	// A band name with "&" is one credit.
	got = trackCredits(&MusicTags{Artist: "Simon & Garfunkel", Title: "The Boxer"}, nil)
	if len(got) != 1 || got[0].Name != "Simon & Garfunkel" || got[0].Featured {
		t.Errorf("band credit = %+v", got)
	}

	// Duplicates fold case-insensitively.
	got = trackCredits(&MusicTags{Artist: "A feat. B", Title: "Song (feat. b)"}, nil)
	if len(got) != 2 {
		t.Errorf("duplicate guest listed twice: %+v", got)
	}
}

func TestAlbumArtistName(t *testing.T) {
	for in, want := range map[string]string{
		"Jay-Z feat. Rihanna": "Jay-Z",
		"Jay-Z (feat. Rihanna)": "Jay-Z",
		"Earth, Wind & Fire":    "Earth, Wind & Fire",
		"Various Artists":       "Various Artists",
	} {
		if got := albumArtistName(in); got != want {
			t.Errorf("albumArtistName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMultiValueTagParsing(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if got := parseMBIDList(a.String() + "\x00" + b.String()); !reflect.DeepEqual(got, []uuid.UUID{a, b}) {
		t.Errorf("NUL-separated MBIDs = %v", got)
	}
	if got := parseMBIDList(a.String() + "/" + b.String()); !reflect.DeepEqual(got, []uuid.UUID{a, b}) {
		t.Errorf("slash-separated MBIDs = %v", got)
	}
	// The single-id accessor keeps working on a multi-valued tag.
	if got := parseMBID(a.String() + ";" + b.String()); got != a {
		t.Errorf("parseMBID on a list = %v, want the first", got)
	}
	// An artist list keeps "AC/DC" whole.
	if got := splitMultiValue("AC/DC\x00Axl Rose", false); !reflect.DeepEqual(got, []string{"AC/DC", "Axl Rose"}) {
		t.Errorf("artists = %q", got)
	}
}

func TestWithID3UserText(t *testing.T) {
	mbid := uuid.New()
	raw := map[string]interface{}{
		"TIT2":   "Song",
		"TXXX":   &tag.Comm{Description: "MusicBrainz Artist Id", Text: mbid.String()},
		"TXXX_0": &tag.Comm{Description: "REPLAYGAIN_TRACK_GAIN", Text: "-6.50 dB"},
		"TXXX_1": &tag.Comm{Description: "ARTISTS", Text: "Jay-Z\x00Rihanna"},
	}
	m := &MusicTags{}
	m.enrichFromRaw(raw)
	if m.MBArtistID != mbid {
		t.Errorf("MP3 artist MBID not read: %v", m.MBArtistID)
	}
	if m.ReplayGainTrackGain == nil || *m.ReplayGainTrackGain != -6.5 {
		t.Errorf("MP3 ReplayGain not read: %v", m.ReplayGainTrackGain)
	}
	if !reflect.DeepEqual(m.Artists, []string{"Jay-Z", "Rihanna"}) {
		t.Errorf("MP3 ARTISTS = %q", m.Artists)
	}
}

// flacWithComments builds a minimal FLAC header: magic, a STREAMINFO-sized
// block to skip, then a Vorbis comment block.
func flacWithComments(comments ...string) []byte {
	var vc bytes.Buffer
	le := func(n int) { _ = binary.Write(&vc, binary.LittleEndian, uint32(n)) }
	le(len("vendor"))
	vc.WriteString("vendor")
	le(len(comments))
	for _, c := range comments {
		le(len(c))
		vc.WriteString(c)
	}
	var b bytes.Buffer
	b.WriteString("fLaC")
	b.Write([]byte{0x00, 0x00, 0x00, 34}) // STREAMINFO, 34 bytes
	b.Write(make([]byte, 34))
	n := vc.Len()
	b.Write([]byte{0x84, byte(n >> 16), byte(n >> 8), byte(n)}) // last block, VORBIS_COMMENT
	b.Write(vc.Bytes())
	return b.Bytes()
}

func TestReadFLACCommentsKeepsRepeatedFields(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	data := flacWithComments(
		"ARTIST=Jay-Z feat. Rihanna",
		"ARTISTS=Jay-Z", "ARTISTS=Rihanna",
		"MUSICBRAINZ_ARTISTID="+a.String(), "musicbrainz_artistid="+b.String(),
	)
	c, err := readFLACComments(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c["artists"], []string{"Jay-Z", "Rihanna"}) {
		t.Errorf("artists = %q", c["artists"])
	}
	m := &MusicTags{Artist: "Rihanna"} // what a last-value-wins reader gives
	m.applyMultiValued(c)
	if !reflect.DeepEqual(m.Artists, []string{"Jay-Z", "Rihanna"}) || !reflect.DeepEqual(m.ArtistMBIDs, []uuid.UUID{a, b}) {
		t.Errorf("multi-valued = %q %v", m.Artists, m.ArtistMBIDs)
	}
	if m.MBArtistID != a {
		t.Errorf("first artist id = %v", m.MBArtistID)
	}

	// Two ARTIST fields and no ARTISTS: the first is the artist, both are
	// credited.
	m = &MusicTags{Artist: "B"}
	c, _ = readFLACComments(bytes.NewReader(flacWithComments("ARTIST=A", "ARTIST=B")))
	m.applyMultiValued(c)
	if m.Artist != "A" || !reflect.DeepEqual(m.Artists, []string{"A", "B"}) {
		t.Errorf("repeated ARTIST: %q %q", m.Artist, m.Artists)
	}

	if _, err := readFLACComments(bytes.NewReader([]byte("ID3\x03junk"))); err == nil {
		t.Error("a non-FLAC stream must be refused")
	}
	// A length field pointing past the data is an error, not a panic.
	bad := flacWithComments("ARTIST=A")
	bad[len(bad)-12] = 0xff
	if _, err := readFLACComments(bytes.NewReader(bad)); err == nil {
		t.Error("a truncated comment block must be refused")
	}
}

// fakeCreditStore records credits per track.
type fakeCreditStore struct {
	byMBID  map[uuid.UUID]uuid.UUID
	credits map[uuid.UUID][]TrackArtist
	scanned map[uuid.UUID]bool
}

func newFakeCreditStore() *fakeCreditStore {
	return &fakeCreditStore{byMBID: map[uuid.UUID]uuid.UUID{}, credits: map[uuid.UUID][]TrackArtist{}, scanned: map[uuid.UUID]bool{}}
}

func (f *fakeCreditStore) FindArtistByMusicBrainzID(_ context.Context, _, mbid uuid.UUID) (uuid.UUID, bool, error) {
	id, ok := f.byMBID[mbid]
	return id, ok, nil
}

func (f *fakeCreditStore) SetTrackArtists(_ context.Context, trackID uuid.UUID, c []TrackArtist) error {
	f.credits[trackID] = append([]TrackArtist(nil), c...)
	return nil
}

func (f *fakeCreditStore) CreditsScanned(_ context.Context, id uuid.UUID) (bool, error) {
	return f.scanned[id], nil
}

func (f *fakeCreditStore) MarkCreditsScanned(_ context.Context, id uuid.UUID) error {
	f.scanned[id] = true
	return nil
}

// creditTagMetadata is stubTagMetadata with a separate album artist.
type creditTagMetadata struct {
	stubTagMetadata
	albumArtist string
}

func (c *creditTagMetadata) AlbumArtist() string { return c.albumArtist }

func TestProcessMusicHierarchy_StoresFeaturedCredits(t *testing.T) {
	svc := newMockMediaService()
	// Resolve existing rows the way the real service does, so a second
	// scan finds what the first created.
	svc.hierarchyFind = func(p media.CreateItemParams) *media.Item {
		for _, it := range svc.items {
			if it.Type == p.Type && it.Title == p.Title && reflect.DeepEqual(it.ParentID, p.ParentID) {
				return it
			}
		}
		return nil
	}
	credits := newFakeCreditStore()
	s := newTestScanner(svc).WithArtistCredits(credits)
	libID := uuid.New()
	// A compilation performer the library already knows.
	known := &media.Item{ID: uuid.New(), LibraryID: libID, Type: "artist", Title: "Kanye West"}
	svc.items[known.ID] = known

	dir := t.TempDir()
	path := filepath.Join(dir, "01 - Run This Town.mp3")
	if err := os.WriteFile(path, []byte("fake audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := readTagFrom
	readTagFrom = func(io.ReadSeeker) (tag.Metadata, error) {
		return &creditTagMetadata{
			stubTagMetadata: stubTagMetadata{artist: "Jay-Z feat. Rihanna & Kanye West", album: "The Blueprint 3", title: "Run This Town", trackN: 1},
			// No ALBUMARTIST: it defaults to the artist tag, feat. clause
			// and all — the album must still hang under Jay-Z.
			albumArtist: "",
		}, nil
	}
	defer func() { readTagFrom = orig }()

	scan := func() *media.Item {
		track, tags, err := s.processMusicHierarchy(context.Background(), libID, path, []string{dir})
		if err != nil {
			t.Fatal(err)
		}
		if !tags.creditsStored {
			t.Fatal("credits not stored")
		}
		return track
	}
	track := scan()

	var artist *media.Item
	for _, it := range svc.items {
		if it.Type == "artist" && it.Title == "Jay-Z" {
			artist = it
		}
		if it.Type == "artist" && strings.Contains(it.Title, "feat.") {
			t.Errorf("an artist row carries the feat. clause: %q", it.Title)
		}
	}
	if artist == nil {
		t.Fatal("album artist Jay-Z not created")
	}
	got := credits.credits[track.ID]
	if len(got) != 2 {
		t.Fatalf("credits = %+v, want Rihanna (created) and Kanye West (existing)", got)
	}
	rihanna := svc.items[got[0].ArtistID]
	if rihanna == nil || rihanna.Title != "Rihanna" || got[0].Role != CreditFeatured {
		t.Errorf("first credit = %+v (%v)", got[0], rihanna)
	}
	if got[1].ArtistID != known.ID || got[1].Role != CreditFeatured {
		t.Errorf("second credit = %+v, want the existing Kanye West", got[1])
	}
	for _, c := range got {
		if c.ArtistID == artist.ID {
			t.Error("the album artist must not be credited on its own track")
		}
	}

	// A second scan of the same file reuses the artists: no new rows, the
	// same credits.
	before := len(svc.items)
	scan()
	if len(svc.items) != before {
		t.Errorf("rescan created %d new items", len(svc.items)-before)
	}
	if again := credits.credits[track.ID]; !reflect.DeepEqual(again, got) {
		t.Errorf("rescan credits = %+v, want %+v", again, got)
	}
}

func TestHealTrackCredits_ReadsOncePerFile(t *testing.T) {
	svc := newMockMediaService()
	credits := newFakeCreditStore()
	s := newTestScanner(svc).WithArtistCredits(credits)
	libID := uuid.New()
	artist := &media.Item{ID: uuid.New(), LibraryID: libID, Type: "artist", Title: "Daft Punk"}
	album := &media.Item{ID: uuid.New(), LibraryID: libID, Type: "album", Title: "RAM", ParentID: &artist.ID}
	track := &media.Item{ID: uuid.New(), LibraryID: libID, Type: "track", Title: "Get Lucky", ParentID: &album.ID}
	for _, it := range []*media.Item{artist, album, track} {
		svc.items[it.ID] = it
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "08 - Get Lucky.flac")
	if err := os.WriteFile(path, []byte("fake audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	reads := 0
	orig := readTagFrom
	readTagFrom = func(io.ReadSeeker) (tag.Metadata, error) {
		reads++
		return &stubTagMetadata{artist: "Daft Punk feat. Pharrell Williams & Nile Rodgers", album: "RAM", title: "Get Lucky"}, nil
	}
	defer func() { readTagFrom = orig }()

	file := &media.File{ID: uuid.New(), MediaItemID: track.ID}
	s.healTrackCredits(context.Background(), libID, track, file, path)
	if n := len(credits.credits[track.ID]); n != 2 {
		t.Fatalf("healed credits = %d, want 2", n)
	}
	if !credits.scanned[file.ID] {
		t.Error("file not marked read")
	}
	s.healTrackCredits(context.Background(), libID, track, file, path)
	// A fresh process (no in-memory memo) still skips it: the mark is in
	// the store.
	s2 := newTestScanner(svc).WithArtistCredits(credits)
	s2.healTrackCredits(context.Background(), libID, track, file, path)
	if reads != 1 {
		t.Errorf("tags read %d times, want once", reads)
	}
}
