package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/domain/people"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/scanner"
)

// peopleItemLookup bridges the v1.PeopleItemLookup interface to the media
// service. The credits handler needs only type + tmdb_id to drive the lazy
// TMDB fetch; full item data isn't needed.
type peopleItemLookup struct {
	svc      *media.Service
	agentFn  func() metadata.Agent
	enricher *scanner.Enricher
	logger   *slog.Logger
}

func (l *peopleItemLookup) GetItemTypeAndTMDB(ctx context.Context, id uuid.UUID) (string, *int, error) {
	item, err := l.svc.GetItem(ctx, id)
	if err != nil {
		return "", nil, err
	}
	return item.Type, item.TMDBID, nil
}

// ResolveTMDBID auto-heals items missing a tmdb_id by searching TMDB by
// title+year and persisting the match. Used so the lazy credit fetch can
// work on libraries scanned before tmdb_id was reliably stored.
func (l *peopleItemLookup) ResolveTMDBID(ctx context.Context, id uuid.UUID) (*int, error) {
	item, err := l.svc.GetItem(ctx, id)
	if err != nil {
		return nil, err
	}
	if item.TMDBID != nil && *item.TMDBID != 0 {
		return item.TMDBID, nil
	}
	if item.Type != "movie" && item.Type != "show" {
		return nil, nil
	}
	agent := l.agentFn()
	if agent == nil || l.enricher == nil {
		return nil, nil
	}

	year := 0
	if item.Year != nil {
		year = *item.Year
	}
	var tmdbID int
	switch item.Type {
	case "movie":
		res, err := agent.SearchMovie(ctx, item.Title, year)
		if err != nil || res == nil {
			return nil, err
		}
		tmdbID = res.TMDBID
	case "show":
		res, err := agent.SearchTV(ctx, item.Title, year)
		if err != nil || res == nil {
			return nil, err
		}
		tmdbID = res.TMDBID
	}
	if tmdbID == 0 {
		return nil, nil
	}
	if err := l.enricher.MatchItem(ctx, id, tmdbID); err != nil {
		l.logger.Warn("auto-resolve tmdb match failed", "item_id", id, "tmdb_id", tmdbID, "err", err)
		return nil, err
	}
	l.logger.Info("auto-resolved tmdb id", "item_id", id, "tmdb_id", tmdbID, "title", item.Title)
	return &tmdbID, nil
}

type peopleAdapter struct{ q *gen.Queries }

func (a *peopleAdapter) GetPersonByID(ctx context.Context, id uuid.UUID) (people.Person, error) {
	r, err := a.q.GetPersonByID(ctx, id)
	if err != nil {
		return people.Person{}, err
	}
	return genPersonToDomain(r), nil
}

func (a *peopleAdapter) GetPersonByTMDBID(ctx context.Context, tmdbID int) (people.Person, error) {
	id32 := int32(tmdbID)
	r, err := a.q.GetPersonByTMDBID(ctx, &id32)
	if err != nil {
		return people.Person{}, err
	}
	return genPersonToDomain(r), nil
}

func (a *peopleAdapter) UpsertPersonByTMDB(ctx context.Context, p people.Person) (people.Person, error) {
	var tmdbID *int32
	if p.TMDBID != nil {
		v := int32(*p.TMDBID)
		tmdbID = &v
	}
	r, err := a.q.UpsertPersonByTMDB(ctx, gen.UpsertPersonByTMDBParams{
		TmdbID:       tmdbID,
		Name:         p.Name,
		ProfilePath:  p.ProfilePath,
		Bio:          p.Bio,
		Birthday:     timePtrToPgDate(p.Birthday),
		Deathday:     timePtrToPgDate(p.Deathday),
		PlaceOfBirth: p.PlaceOfBirth,
	})
	if err != nil {
		return people.Person{}, err
	}
	return genPersonToDomain(r), nil
}

func (a *peopleAdapter) SearchPeople(ctx context.Context, prefix string, limit int32) ([]people.Summary, error) {
	rows, err := a.q.SearchPeople(ctx, gen.SearchPeopleParams{Prefix: prefix, LimitN: limit})
	if err != nil {
		return nil, err
	}
	out := make([]people.Summary, len(rows))
	for i, r := range rows {
		out[i] = people.Summary{
			ID:          r.ID,
			TMDBID:      int32PtrToIntPtr(r.TmdbID),
			Name:        r.Name,
			ProfilePath: r.ProfilePath,
		}
	}
	return out, nil
}

func (a *peopleAdapter) ListCreditsForItem(ctx context.Context, itemID uuid.UUID) ([]people.Credit, error) {
	rows, err := a.q.ListCreditsForItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := make([]people.Credit, len(rows))
	for i, r := range rows {
		character := ""
		if r.Character != nil {
			character = *r.Character
		}
		out[i] = people.Credit{
			Person: people.Summary{
				ID:          r.PersonID,
				TMDBID:      int32PtrToIntPtr(r.TmdbID),
				Name:        r.Name,
				ProfilePath: r.ProfilePath,
			},
			Role:      r.Role,
			Character: character,
			Job:       r.Job,
			Order:     int(r.Ord),
		}
	}
	return out, nil
}

func (a *peopleAdapter) ListFilmographyForPerson(ctx context.Context, personID uuid.UUID) ([]people.FilmographyEntry, error) {
	rows, err := a.q.ListFilmographyForPerson(ctx, personID)
	if err != nil {
		return nil, err
	}
	out := make([]people.FilmographyEntry, len(rows))
	for i, r := range rows {
		character := ""
		if r.Character != nil {
			character = *r.Character
		}
		out[i] = people.FilmographyEntry{
			ItemID:     r.ID,
			LibraryID:  r.LibraryID,
			Title:      r.Title,
			Type:       r.Type,
			Year:       int32PtrToIntPtr(r.Year),
			PosterPath: r.PosterPath,
			Rating:     numericToFloat64Ptr(r.Rating),
			Role:       r.Role,
			Character:  character,
			Job:        r.Job,
		}
	}
	return out, nil
}

func (a *peopleAdapter) DeleteCreditsForItem(ctx context.Context, itemID uuid.UUID) error {
	return a.q.DeleteCreditsForItem(ctx, itemID)
}

func (a *peopleAdapter) InsertCredit(ctx context.Context, itemID, personID uuid.UUID, role, character, job string, ord int32) error {
	var charPtr *string
	if character != "" {
		charPtr = &character
	}
	return a.q.InsertCredit(ctx, gen.InsertCreditParams{
		MediaItemID: itemID,
		PersonID:    personID,
		Role:        role,
		Character:   charPtr,
		Job:         job,
		Ord:         ord,
	})
}

func genPersonToDomain(r gen.Person) people.Person {
	return people.Person{
		ID:           r.ID,
		TMDBID:       int32PtrToIntPtr(r.TmdbID),
		Name:         r.Name,
		ProfilePath:  r.ProfilePath,
		Bio:          r.Bio,
		Birthday:     pgDateToTimePtr(r.Birthday),
		Deathday:     pgDateToTimePtr(r.Deathday),
		PlaceOfBirth: r.PlaceOfBirth,
	}
}

func pgDateToTimePtr(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	t := d.Time
	return &t
}

func timePtrToPgDate(t *time.Time) pgtype.Date {
	if t == nil || t.IsZero() {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: *t, Valid: true}
}
