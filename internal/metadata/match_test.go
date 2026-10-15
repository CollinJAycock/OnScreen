package metadata

import (
	"fmt"
	"testing"
)

func TestMatchTitle(t *testing.T) {
	c := func(year int, titles ...string) Candidate { return Candidate{Titles: titles, Year: year} }
	for _, tc := range []struct {
		name  string
		title string
		year  int
		cands []Candidate
		want  []int
	}{
		{"exact year beats a year off", "Example Film", 2021,
			[]Candidate{c(2020, "Example Film"), c(2021, "Example Film")}, []int{1}},
		{"a year off when nothing is exact", "Example Film", 2021,
			[]Candidate{c(2020, "Example Film"), c(2023, "Example Film")}, []int{0}},
		{"two years off is refused", "Example Film", 2021,
			[]Candidate{c(2019, "Example Film"), c(2023, "Example Film")}, nil},
		{"no year on the candidate is refused when a year was parsed", "Example Film", 2021,
			[]Candidate{c(0, "Example Film")}, nil},
		{"no parsed year takes any year", "Example Film", 0,
			[]Candidate{c(2024, "Example Film Part Two"), c(1984, "Example Film")}, []int{1}},
		{"partial titles are refused", "Spring", 2019,
			[]Candidate{c(2019, "Spring Breakers"), c(2019, "Spring Fever")}, nil},
		{"title inside a longer title is refused", "Hero", 2018,
			[]Candidate{c(2018, "Chestnut: Hero of Central Park")}, nil},
		{"longer query than title is refused", "Dr STONE NEW WORLD", 0,
			[]Candidate{c(2019, "Dr. STONE")}, nil},
		{"any title counts", "万引き家族", 2018,
			[]Candidate{c(2018, "Shoplifters", "万引き家族")}, []int{0}},
		{"ties keep the order given", "Singularity", 2026,
			[]Candidate{c(2026, "Singularity"), c(2026, "Singularity")}, []int{0, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchTitle(tc.title, tc.year, tc.cands); fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFoldTitle(t *testing.T) {
	same := [][2]string{
		// Filename-derived title vs TMDB's, for the films a fresh server
		// matched correctly on 2026-10-01.
		{"Big Buck Bunny", "Big Buck Bunny"},
		{"Elephants Dream", "Elephants Dream"},
		{"Wing It", "Wing It!"},
		{"Coffee Run", "Coffee Run"},
		{"Tears of Steel", "Tears of Steel"},
		{"Hero", "HERO"},
		{"Spider Man", "Spider-Man"},
		{"Spiderman", "Spider-Man"},
		{"S W A T", "S.W.A.T."},
		{"Amelie", "Amélie"},
		{"Fast and Furious", "Fast & Furious"},
		{"Avengers", "The Avengers"},
		{"Schindlers List", "Schindler's List"},
		{"Mission Impossible Fallout", "Mission: Impossible - Fallout"},
		{"Law and Order SVU", "Law & Order: SVU"},
		{"ＡＫＩＲＡ", "AKIRA"},
	}
	for _, p := range same {
		if FoldTitle(p[0]) != FoldTitle(p[1]) {
			t.Errorf("%q and %q should fold alike: %q vs %q", p[0], p[1], FoldTitle(p[0]), FoldTitle(p[1]))
		}
	}
	differ := [][2]string{
		{"Spring", "Spring Breakers"},
		{"Hero", "Chestnut: Hero of Central Park"},
		{"Singularity", "The Singularity Protocol"},
		{"The Office", "The Office (US)"},
		{"千と千尋の神隠し", "もののけ姫"},
		{"Кино", "Сталкер"},
	}
	for _, p := range differ {
		if FoldTitle(p[0]) == FoldTitle(p[1]) {
			t.Errorf("%q and %q must not fold alike (both %q)", p[0], p[1], FoldTitle(p[0]))
		}
	}
	if FoldTitle("千と千尋の神隠し") == "" {
		t.Error("a title in another script must not fold to nothing")
	}
}

func TestSplitShowQualifier(t *testing.T) {
	for _, tc := range []struct {
		in, title, country string
		year               int
	}{
		{"The Office (US)", "The Office", "US", 0},
		{"The Office (UK)", "The Office", "GB", 0},
		{"Doctor Who (2005)", "Doctor Who", "", 2005},
		{"Shameless (US)", "Shameless", "US", 0},
		{"Breaking Bad", "Breaking Bad", "", 0},
		// Not qualifiers: a lowercase or longer parenthetical is title text,
		// and so is anything not at the end.
		{"Kaguya-sama (Love is War)", "Kaguya-sama (Love is War)", "", 0},
		{"(500) Days of Summer", "(500) Days of Summer", "", 0},
		{"Show (us)", "Show (us)", "", 0},
	} {
		title, country, year := SplitShowQualifier(tc.in)
		if title != tc.title || country != tc.country || year != tc.year {
			t.Errorf("SplitShowQualifier(%q) = %q, %q, %d; want %q, %q, %d",
				tc.in, title, country, year, tc.title, tc.country, tc.year)
		}
	}
}
