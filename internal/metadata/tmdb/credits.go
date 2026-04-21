package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/onscreen/onscreen/internal/metadata"
)

// MovieCredits returns cast and crew for a movie. Crew is filtered to the
// roles users actually care about (director, writer, producer) — TMDB returns
// dozens of jobs per film and most are noise on a detail page.
func (c *Client) MovieCredits(ctx context.Context, tmdbID int) (*metadata.CreditsResult, error) {
	var resp tmdbCredits
	path := fmt.Sprintf("/movie/%d/credits", tmdbID)
	if err := c.get(ctx, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("tmdb movie credits %d: %w", tmdbID, err)
	}
	return c.toCreditsResult(ctx, resp), nil
}

// TVCredits returns aggregated cast and crew for a TV series.
// /tv/{id}/aggregate_credits sums across episodes; /tv/{id}/credits is just
// the show-level credits. Aggregate is what users expect on a series page.
func (c *Client) TVCredits(ctx context.Context, tmdbID int) (*metadata.CreditsResult, error) {
	var resp tmdbAggregateCredits
	path := fmt.Sprintf("/tv/%d/aggregate_credits", tmdbID)
	if err := c.get(ctx, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("tmdb tv credits %d: %w", tmdbID, err)
	}
	out := &metadata.CreditsResult{}
	for _, m := range resp.Cast {
		character := ""
		if len(m.Roles) > 0 {
			character = m.Roles[0].Character
		}
		out.Cast = append(out.Cast, metadata.CreditMember{
			TMDBID:      m.ID,
			Name:        m.Name,
			ProfilePath: m.ProfilePath,
			Character:   character,
			Order:       m.Order,
		})
	}
	for _, m := range resp.Crew {
		for _, j := range m.Jobs {
			role := mapJobToRole(j.Job)
			if role == "" {
				continue
			}
			out.Crew = append(out.Crew, metadata.CreditMember{
				TMDBID:      m.ID,
				Name:        m.Name,
				ProfilePath: m.ProfilePath,
				Role:        role,
				Job:         j.Job,
			})
		}
	}
	return out, nil
}

// PersonDetails fetches biography metadata. Best-effort: returned errors
// should be treated as "no enrichment available, use what we have".
func (c *Client) PersonDetails(ctx context.Context, tmdbID int) (*metadata.PersonResult, error) {
	var p tmdbPerson
	path := fmt.Sprintf("/person/%d", tmdbID)
	params := url.Values{}
	params.Set("language", c.language)
	if err := c.get(ctx, path, params, &p); err != nil {
		return nil, fmt.Errorf("tmdb person %d: %w", tmdbID, err)
	}
	birthday, _ := time.Parse("2006-01-02", p.Birthday)
	deathday, _ := time.Parse("2006-01-02", p.Deathday)
	return &metadata.PersonResult{
		TMDBID:       p.ID,
		Name:         p.Name,
		Bio:          p.Biography,
		ProfilePath:  p.ProfilePath,
		Birthday:     birthday,
		Deathday:     deathday,
		PlaceOfBirth: p.PlaceOfBirth,
	}, nil
}

func (c *Client) toCreditsResult(_ context.Context, r tmdbCredits) *metadata.CreditsResult {
	out := &metadata.CreditsResult{}
	for _, m := range r.Cast {
		out.Cast = append(out.Cast, metadata.CreditMember{
			TMDBID:      m.ID,
			Name:        m.Name,
			ProfilePath: m.ProfilePath,
			Character:   m.Character,
			Order:       m.Order,
		})
	}
	for _, m := range r.Crew {
		role := mapJobToRole(m.Job)
		if role == "" {
			continue
		}
		out.Crew = append(out.Crew, metadata.CreditMember{
			TMDBID:      m.ID,
			Name:        m.Name,
			ProfilePath: m.ProfilePath,
			Role:        role,
			Job:         m.Job,
		})
	}
	return out
}

// mapJobToRole groups TMDB's many crew job titles into the four buckets
// surfaced in the UI. Anything not listed is dropped.
func mapJobToRole(job string) string {
	switch job {
	case "Director":
		return "director"
	case "Writer", "Screenplay", "Story", "Teleplay":
		return "writer"
	case "Producer", "Executive Producer":
		return "producer"
	case "Creator":
		return "creator"
	}
	return ""
}

type tmdbCredits struct {
	Cast []tmdbCastMember `json:"cast"`
	Crew []tmdbCrewMember `json:"crew"`
}

type tmdbCastMember struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	ProfilePath string `json:"profile_path"`
	Order       int    `json:"order"`
}

type tmdbCrewMember struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Job         string `json:"job"`
	ProfilePath string `json:"profile_path"`
}

type tmdbAggregateCredits struct {
	Cast []tmdbAggCast `json:"cast"`
	Crew []tmdbAggCrew `json:"crew"`
}

type tmdbAggCast struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	ProfilePath string `json:"profile_path"`
	Order       int    `json:"order"`
	Roles       []struct {
		Character string `json:"character"`
	} `json:"roles"`
}

type tmdbAggCrew struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	ProfilePath string `json:"profile_path"`
	Jobs        []struct {
		Job string `json:"job"`
	} `json:"jobs"`
}

type tmdbPerson struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Biography    string `json:"biography"`
	ProfilePath  string `json:"profile_path"`
	Birthday     string `json:"birthday"`
	Deathday     string `json:"deathday"`
	PlaceOfBirth string `json:"place_of_birth"`
}
