// Package people contains business logic for cast and crew metadata.
//
// Credits are populated lazily: the first time a user opens an item's detail
// page, GetCredits sees an empty result, fetches from TMDB, and persists the
// rows for future requests. There is no batch backfill — large libraries pay
// the TMDB call only when someone actually looks at an item.
package people

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/metadata"
)

var ErrNotFound = errors.New("person not found")

// Person is the public domain model.
type Person struct {
	ID           uuid.UUID
	TMDBID       *int
	Name         string
	ProfilePath  *string
	Bio          *string
	Birthday     *time.Time
	Deathday     *time.Time
	PlaceOfBirth *string
}

// Summary is the lightweight projection used in lists and credit rows.
type Summary struct {
	ID          uuid.UUID
	TMDBID      *int
	Name        string
	ProfilePath *string
}

// Credit is one cast or crew entry on an item.
type Credit struct {
	Person    Summary
	Role      string // "cast" | "director" | "writer" | "producer" | "creator"
	Character string // cast only
	Job       string // crew only
	Order     int
}

// FilmographyEntry is one item a person worked on.
type FilmographyEntry struct {
	ItemID     uuid.UUID
	LibraryID  uuid.UUID
	Title      string
	Type       string
	Year       *int
	PosterPath *string
	Rating     *float64
	Role       string
	Character  string
	Job        string
}

// Querier is the subset of generated DB calls this service needs.
// Adapter translates gen rows into domain types.
type Querier interface {
	GetPersonByID(ctx context.Context, id uuid.UUID) (Person, error)
	GetPersonByTMDBID(ctx context.Context, tmdbID int) (Person, error)
	UpsertPersonByTMDB(ctx context.Context, p Person) (Person, error)
	SearchPeople(ctx context.Context, prefix string, limit int32) ([]Summary, error)

	ListCreditsForItem(ctx context.Context, itemID uuid.UUID) ([]Credit, error)
	ListFilmographyForPerson(ctx context.Context, personID uuid.UUID) ([]FilmographyEntry, error)
	DeleteCreditsForItem(ctx context.Context, itemID uuid.UUID) error
	InsertCredit(ctx context.Context, itemID, personID uuid.UUID, role, character, job string, ord int32) error
}

// Agent is the subset of metadata.Agent used to lazy-fetch credits.
// *tmdb.Client satisfies this; tests use a fake.
type Agent interface {
	MovieCredits(ctx context.Context, tmdbID int) (*metadata.CreditsResult, error)
	TVCredits(ctx context.Context, tmdbID int) (*metadata.CreditsResult, error)
	PersonDetails(ctx context.Context, tmdbID int) (*metadata.PersonResult, error)
}

// Service is the business-logic entry point.
type Service struct {
	q       Querier
	agentFn func() Agent
}

// New constructs a Service. agentFn is called per request and may return nil
// when no provider is configured — in that case lazy fetch is skipped and
// GetCredits returns whatever is already in the DB.
func New(q Querier, agentFn func() Agent) *Service {
	if agentFn == nil {
		agentFn = func() Agent { return nil }
	}
	return &Service{q: q, agentFn: agentFn}
}

func (s *Service) agent() Agent { return s.agentFn() }

// GetCredits returns credits for an item. If the DB has none and the item has
// a TMDB id and we have an agent configured, the credits are fetched from
// TMDB synchronously, persisted, and returned. Errors during the lazy fetch
// are logged but not surfaced — the caller still gets the (empty) DB result.
func (s *Service) GetCredits(ctx context.Context, itemID uuid.UUID, itemType string, tmdbID *int) ([]Credit, error) {
	credits, err := s.q.ListCreditsForItem(ctx, itemID)
	if err != nil {
		return nil, fmt.Errorf("list credits: %w", err)
	}
	if len(credits) > 0 {
		return credits, nil
	}
	agent := s.agent()
	if agent == nil {
		slog.Info("skip lazy credit fetch: no metadata agent", "item_id", itemID, "item_type", itemType)
		return credits, nil
	}
	if tmdbID == nil || *tmdbID == 0 {
		slog.Info("skip lazy credit fetch: item has no tmdb_id", "item_id", itemID, "item_type", itemType)
		return credits, nil
	}
	if itemType != "movie" && itemType != "show" {
		slog.Info("skip lazy credit fetch: unsupported item type", "item_id", itemID, "item_type", itemType, "tmdb_id", *tmdbID)
		return credits, nil
	}
	slog.Info("lazy credit fetch start", "item_id", itemID, "item_type", itemType, "tmdb_id", *tmdbID)
	if err := s.fetchAndStoreCredits(ctx, agent, itemID, itemType, *tmdbID); err != nil {
		slog.Warn("lazy credit fetch failed", "item_id", itemID, "tmdb_id", *tmdbID, "err", err)
		return credits, nil
	}
	out, err := s.q.ListCreditsForItem(ctx, itemID)
	if err != nil {
		return nil, fmt.Errorf("re-list credits after fetch: %w", err)
	}
	slog.Info("lazy credit fetch complete", "item_id", itemID, "credit_count", len(out))
	return out, nil
}

func (s *Service) fetchAndStoreCredits(ctx context.Context, agent Agent, itemID uuid.UUID, itemType string, tmdbID int) error {
	var (
		res *metadata.CreditsResult
		err error
	)
	switch itemType {
	case "movie":
		res, err = agent.MovieCredits(ctx, tmdbID)
	case "show":
		res, err = agent.TVCredits(ctx, tmdbID)
	default:
		return nil // episodes/seasons inherit credits from their parent show
	}
	if err != nil {
		return fmt.Errorf("agent: %w", err)
	}

	// Cap cast at 50 — beyond that is mostly extras users won't care about
	// and would bloat the people table on a busy library.
	const maxCast = 50
	cast := res.Cast
	if len(cast) > maxCast {
		cast = cast[:maxCast]
	}

	for _, m := range cast {
		if err := s.upsertAndLink(ctx, itemID, m, "cast"); err != nil {
			return err
		}
	}
	for _, m := range res.Crew {
		role := m.Role
		if role == "" {
			continue
		}
		if err := s.upsertAndLink(ctx, itemID, m, role); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) upsertAndLink(ctx context.Context, itemID uuid.UUID, m metadata.CreditMember, role string) error {
	person := Person{
		TMDBID:      &m.TMDBID,
		Name:        m.Name,
		ProfilePath: stringPtr(m.ProfilePath),
	}
	saved, err := s.q.UpsertPersonByTMDB(ctx, person)
	if err != nil {
		return fmt.Errorf("upsert person %s: %w", m.Name, err)
	}
	character := m.Character
	job := m.Job
	if role != "cast" {
		// crew rows must have non-empty job to keep PK distinct between
		// e.g. "Writer" and "Director" credits for the same person.
		if job == "" {
			job = capitalize(role)
		}
	}
	return s.q.InsertCredit(ctx, itemID, saved.ID, role, character, job, int32(m.Order))
}

// GetPerson returns the person record. If the person has a TMDB id and bio
// is missing, the bio is fetched from TMDB on demand.
func (s *Service) GetPerson(ctx context.Context, id uuid.UUID) (Person, error) {
	p, err := s.q.GetPersonByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Person{}, ErrNotFound
		}
		return Person{}, err
	}
	agent := s.agent()
	if (p.Bio == nil || *p.Bio == "") && p.TMDBID != nil && agent != nil {
		if details, err := agent.PersonDetails(ctx, *p.TMDBID); err == nil {
			enriched := Person{
				TMDBID:       p.TMDBID,
				Name:         details.Name,
				ProfilePath:  stringPtr(details.ProfilePath),
				Bio:          stringPtr(details.Bio),
				Birthday:     timePtr(details.Birthday),
				Deathday:     timePtr(details.Deathday),
				PlaceOfBirth: stringPtr(details.PlaceOfBirth),
			}
			if saved, err := s.q.UpsertPersonByTMDB(ctx, enriched); err == nil {
				return saved, nil
			}
		}
	}
	return p, nil
}

// GetFilmography returns the items a person worked on, grouped by role on
// the caller side.
func (s *Service) GetFilmography(ctx context.Context, personID uuid.UUID) ([]FilmographyEntry, error) {
	return s.q.ListFilmographyForPerson(ctx, personID)
}

// Search returns people whose name starts with the prefix.
func (s *Service) Search(ctx context.Context, prefix string, limit int32) ([]Summary, error) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	return s.q.SearchPeople(ctx, prefix, limit)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
