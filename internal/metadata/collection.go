package metadata

// CollectionRef identifies the TMDB collection (a franchise: "Alien
// Collection", "Toy Story Collection") a movie belongs to. It is what a
// movie-details response carries in belongs_to_collection.
type CollectionRef struct {
	TMDBID      int
	Name        string
	PosterURL   string
	BackdropURL string
}

// CollectionDetail is a TMDB collection with every film in it, as returned by
// /collection/{id}. Parts are in release order; parts without a release date
// (announced, undated) sort last.
type CollectionDetail struct {
	TMDBID      int
	Name        string
	Overview    string
	PosterURL   string
	BackdropURL string
	Parts       []CollectionPart
}

// CollectionPart is one film of a TMDB collection.
type CollectionPart struct {
	TMDBID      int
	Title       string
	ReleaseDate string // YYYY-MM-DD, "" when TMDB has none
	Year        int    // 0 when unknown
	PosterURL   string
	Overview    string
}
