package digest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/email"
	"github.com/onscreen/onscreen/internal/itemlabel"
	"github.com/onscreen/onscreen/internal/userstats"
)

// Store is the query subset the digest uses. Satisfied by *gen.Queries.
type Store interface {
	ListDigestRecipients(ctx context.Context) ([]gen.ListDigestRecipientsRow, error)
	GetDigestRecipient(ctx context.Context, id uuid.UUID) (gen.GetDigestRecipientRow, error)
	ClaimDigestPeriod(ctx context.Context, arg gen.ClaimDigestPeriodParams) (int64, error)
	ReleaseDigestPeriod(ctx context.Context, arg gen.ReleaseDigestPeriodParams) error
	ListDigestNewItems(ctx context.Context, arg gen.ListDigestNewItemsParams) ([]gen.ListDigestNewItemsRow, error)
	ListContinueWatching(ctx context.Context, arg gen.ListContinueWatchingParams) ([]gen.ListContinueWatchingRow, error)
}

// LibraryAccess resolves which libraries a user can see (nil = every
// library, for admins). Satisfied by *library.Service.
type LibraryAccess interface {
	AllowedLibraryIDs(ctx context.Context, userID uuid.UUID, isAdmin bool) (map[uuid.UUID]struct{}, error)
}

// StatsSource supplies the user's numbers for the period. Satisfied by
// *userstats.Service.
type StatsSource interface {
	TotalsFor(ctx context.Context, v userstats.Viewer, r userstats.Range, tz string) (userstats.Totals, error)
}

// Mailer sends the email. Satisfied by *email.Sender.
type Mailer interface {
	Enabled(ctx context.Context) bool
	SendMessage(ctx context.Context, m email.Message) error
}

// PosterSource returns a small JPEG of a library-relative artwork path for
// inline attachment. See ArtworkPosters.
type PosterSource interface {
	Poster(ctx context.Context, libraryID uuid.UUID, relPath string) ([]byte, error)
}

// Deps wires a Service.
type Deps struct {
	Store   Store
	Access  LibraryAccess
	Stats   StatsSource  // nil leaves the numbers out
	Posters PosterSource // nil sends no images
	Mailer  Mailer
	Signer  *Signer
	// Config returns the live server-wide settings.
	Config func(ctx context.Context) settings.DigestConfig
	// BaseURL returns the server's public URL for links (the general
	// settings' Base URL, falling back to the startup default).
	BaseURL func(ctx context.Context) string
	// Location is the zone the send day/hour are in (time.Local).
	Location *time.Location
	Logger   *slog.Logger
}

// Service composes and sends digests.
type Service struct {
	d   Deps
	now func() time.Time
	// pace is the pause between two sends, so a large household doesn't
	// burst its relay's rate limit.
	pace time.Duration
}

// New returns a Service.
func New(d Deps) *Service {
	if d.Location == nil {
		d.Location = time.Local
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Service{d: d, now: time.Now, pace: 2 * time.Second}
}

// Errors from SendTest.
var (
	ErrNoEmail       = errors.New("digest: your account has no email address")
	ErrSMTPOff       = errors.New("digest: SMTP is not configured")
	ErrUnknownUser   = errors.New("digest: user not found")
	errNothingToSend = errors.New("digest: nothing new")
)

const (
	perKind       = 8 // titles listed per section
	continueLimit = 4 // Continue Watching titles
	maxImages     = 36
)

// recipient is the common shape of the two recipient queries.
type recipient struct {
	ID               uuid.UUID
	Username         string
	Email            string
	IsAdmin          bool
	MaxContentRating *string
	BlockedLabels    []string
	AllowedLabels    []string
	Frequency        string
	LastPeriod       *string
	LastSentAt       pgtype.Timestamptz
}

// SendDue is the digest_send task: send every opted-in user the digest of
// their current period when it's due and they haven't had it. A no-op when
// the digest is off server-wide or SMTP isn't configured. Returns a summary
// for the task history.
func (s *Service) SendDue(ctx context.Context) (string, error) {
	cfg := s.d.Config(ctx)
	if !cfg.Enabled {
		return "digest email is off (Settings > Email)", nil
	}
	if !s.d.Mailer.Enabled(ctx) {
		return "SMTP is not configured; no digests sent", nil
	}
	rows, err := s.d.Store.ListDigestRecipients(ctx)
	if err != nil {
		return "", fmt.Errorf("digest: list recipients: %w", err)
	}
	now := s.now()
	var sent, empty, failed int
	var lastErr error
	for i, row := range rows {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		r := recipient(row)
		per, due := CurrentPeriod(r.Frequency, cfg, now, s.d.Location)
		if !due || (r.LastPeriod != nil && *r.LastPeriod == per.Key) {
			continue
		}
		// Claim the period first: a run that overlaps this one (or a retry)
		// finds it taken and skips, so nobody gets a period twice.
		n, err := s.d.Store.ClaimDigestPeriod(ctx, gen.ClaimDigestPeriodParams{Period: per.Key, UserID: r.ID})
		if err != nil {
			return "", fmt.Errorf("digest: claim: %w", err)
		}
		if n == 0 {
			continue
		}
		err = s.sendOne(ctx, r, per, cfg, false)
		switch {
		case errors.Is(err, errNothingToSend):
			// Keep the claim: the period is done, there was just nothing in it.
			empty++
			continue
		case err != nil:
			failed++
			lastErr = err
			s.d.Logger.WarnContext(ctx, "digest: send failed", "user_id", r.ID, "period", per.Key, "err", err)
			s.release(ctx, r, per)
			continue
		}
		sent++
		if i < len(rows)-1 && s.pace > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(s.pace):
			}
		}
	}
	summary := fmt.Sprintf("sent %d digest(s), %d with nothing new, %d failed", sent, empty, failed)
	if failed > 0 && sent == 0 {
		return summary, fmt.Errorf("digest: %s: %w", summary, lastErr)
	}
	return summary, nil
}

// release hands a claimed period back after a failed send so the next hourly
// run retries it (within the send grace).
func (s *Service) release(ctx context.Context, r recipient, per Period) {
	if err := s.d.Store.ReleaseDigestPeriod(context.WithoutCancel(ctx), gen.ReleaseDigestPeriodParams{
		PreviousPeriod: r.LastPeriod,
		PreviousSentAt: r.LastSentAt,
		UserID:         r.ID,
		Period:         per.Key,
	}); err != nil {
		s.d.Logger.WarnContext(ctx, "digest: release period", "user_id", r.ID, "err", err)
	}
}

// SendTest sends userID their digest of the current period right away,
// whether or not they opted in, nothing was added, or the digest is on
// server-wide: the admin's "Send me a test digest now". It doesn't touch the
// user's last-sent period.
func (s *Service) SendTest(ctx context.Context, userID uuid.UUID) (string, error) {
	if !s.d.Mailer.Enabled(ctx) {
		return "", ErrSMTPOff
	}
	row, err := s.d.Store.GetDigestRecipient(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnknownUser
	}
	if err != nil {
		return "", fmt.Errorf("digest: recipient: %w", err)
	}
	r := recipient(row)
	if r.Email == "" {
		return "", ErrNoEmail
	}
	freq := r.Frequency
	if freq != FrequencyMonthly {
		freq = FrequencyWeekly
	}
	cfg := s.d.Config(ctx)
	per, _ := CurrentPeriod(freq, cfg, s.now(), s.d.Location)
	if err := s.sendOne(ctx, r, per, cfg, true); err != nil {
		return "", err
	}
	return r.Email, nil
}

// sendOne composes and sends one digest. Without test it returns
// errNothingToSend instead of sending an empty digest.
func (s *Service) sendOne(ctx context.Context, r recipient, per Period, cfg settings.DigestConfig, test bool) error {
	content, images, err := s.compose(ctx, r, per, cfg)
	if err != nil {
		return err
	}
	if content.Empty() && !test {
		return errNothingToSend
	}
	content.Test = test
	headers := map[string]string{}
	if content.UnsubscribeURL != "" {
		headers["List-Unsubscribe"] = "<" + content.UnsubscribeURL + ">"
		headers["List-Unsubscribe-Post"] = "List-Unsubscribe=One-Click"
	}
	return s.d.Mailer.SendMessage(ctx, email.Message{
		To:      []string{r.Email},
		Subject: content.Subject(),
		Text:    content.Text(),
		HTML:    content.HTML(),
		Inline:  images,
		Headers: headers,
	})
}

// viewer is what r may see: their libraries, rating ceiling and labels,
// exactly as their own requests are filtered.
func (s *Service) viewer(ctx context.Context, r recipient) (userstats.Viewer, map[uuid.UUID]struct{}, error) {
	allowed, err := s.d.Access.AllowedLibraryIDs(ctx, r.ID, r.IsAdmin)
	if err != nil {
		return userstats.Viewer{}, nil, fmt.Errorf("digest: allowed libraries: %w", err)
	}
	v := userstats.Viewer{
		UserID:     r.ID,
		LibraryIDs: userstats.LibraryIDList(allowed),
		Labels:     itemlabel.Filter{Blocked: r.BlockedLabels, Allowed: r.AllowedLabels},
	}
	if r.MaxContentRating != nil {
		v.MaxContentRating = *r.MaxContentRating
	}
	return v, allowed, nil
}

func ratingRank(maxRating string) *int32 {
	rk := contentrating.MaxRatingRank(maxRating)
	if rk == nil {
		return nil
	}
	v := int32(*rk)
	return &v
}

// compose gathers a digest's content and its inline posters.
func (s *Service) compose(ctx context.Context, r recipient, per Period, cfg settings.DigestConfig) (Content, []email.InlineImage, error) {
	v, allowed, err := s.viewer(ctx, r)
	if err != nil {
		return Content{}, nil, err
	}
	rank := ratingRank(v.MaxContentRating)
	rows, err := s.d.Store.ListDigestNewItems(ctx, gen.ListDigestNewItemsParams{
		PerKind:       perKind,
		Since:         pgtype.Timestamptz{Time: per.Since, Valid: true},
		Until:         pgtype.Timestamptz{Time: per.Until, Valid: true},
		LibraryIds:    v.LibraryIDs,
		MaxRatingRank: rank,
		BlockedLabels: v.Labels.BlockedArg(),
		AllowedLabels: v.Labels.AllowedArg(),
	})
	if err != nil {
		return Content{}, nil, fmt.Errorf("digest: new items: %w", err)
	}

	base := ""
	if s.d.BaseURL != nil {
		base = cleanBaseURL(s.d.BaseURL(ctx))
	}
	c := Content{Username: r.Username, Period: per, BaseURL: base}
	if base != "" && s.d.Signer != nil {
		c.UnsubscribeURL = base + "/api/v1/digest/unsubscribe?token=" + s.d.Signer.Token(r.ID)
	}

	byKind := map[string]*Section{}
	for _, k := range sectionOrder {
		byKind[k] = &Section{Kind: k, Heading: sectionHeadings[k]}
	}
	for _, row := range rows {
		sec, ok := byKind[row.Kind]
		if !ok {
			continue
		}
		en := Entry{ID: row.RefID, LibraryID: row.LibraryID, ItemType: row.ItemType, Title: row.Title, Year: row.Year}
		if row.PosterPath != nil {
			en.PosterPath = *row.PosterPath
		}
		switch row.Kind {
		case "album":
			en.Subtitle = row.Subtitle
		case "show":
			en.Subtitle = plural(row.ItemCount, "new episode", "new episodes")
		}
		sec.Entries = append(sec.Entries, en)
		// KindTotal counts the kind's entries before the per-kind cut.
		sec.More = max(0, row.KindTotal-perKind)
	}
	for _, k := range sectionOrder {
		c.Sections = append(c.Sections, *byKind[k])
	}

	if cfg.IncludeContinueWatching {
		cw, err := s.d.Store.ListContinueWatching(ctx, gen.ListContinueWatchingParams{
			UserID:        r.ID,
			Limit:         20,
			MaxRatingRank: rank,
			BlockedLabels: v.Labels.BlockedArg(),
			AllowedLabels: v.Labels.AllowedArg(),
		})
		if err != nil {
			return Content{}, nil, fmt.Errorf("digest: continue watching: %w", err)
		}
		for _, row := range cw {
			if allowed != nil {
				if _, ok := allowed[row.LibraryID]; !ok {
					continue
				}
			}
			c.ContinueWatching = append(c.ContinueWatching, continueEntry(row))
			if len(c.ContinueWatching) == continueLimit {
				break
			}
		}
	}

	if cfg.IncludeStats && s.d.Stats != nil {
		tz := s.d.Location.String()
		if _, err := time.LoadLocation(tz); err != nil || tz == "Local" {
			tz = "UTC" // only buckets active days, which the email doesn't show
		}
		t, err := s.d.Stats.TotalsFor(ctx, v, userstats.Range{Key: per.Key, Since: per.Since, Until: per.Until}, tz)
		if err != nil {
			return Content{}, nil, err
		}
		if t.Plays > 0 || t.BooksRead > 0 {
			c.Stats = &t
		}
	}

	images := s.attachPosters(ctx, &c)
	return c, images, nil
}

// continueEntry renders a Continue Watching row: an episode shows as its
// show, with the episode as the subtitle.
func continueEntry(row gen.ListContinueWatchingRow) Entry {
	en := Entry{ID: row.ID, LibraryID: row.LibraryID, ItemType: row.Type, Title: row.Title, Year: row.Year}
	if row.FallbackPoster != nil {
		en.PosterPath = *row.FallbackPoster
	}
	if row.Type == "episode" && row.ShowTitle != nil && row.ShowID.Valid {
		en.Subtitle = row.Title
		if row.Index != nil {
			en.Subtitle = "E" + strconv.Itoa(int(*row.Index)) + " · " + row.Title
		}
		en.Title = *row.ShowTitle
		en.Year = row.ShowYear
	}
	return en
}

// attachPosters loads each entry's poster (bounded by maxImages), sets its
// ContentID and returns the attachments. A poster that can't be loaded just
// leaves its tile as a plain block.
func (s *Service) attachPosters(ctx context.Context, c *Content) []email.InlineImage {
	if s.d.Posters == nil {
		return nil
	}
	var images []email.InlineImage
	byPath := map[string]string{}
	attach := func(en *Entry) {
		if en.PosterPath == "" || len(images) >= maxImages {
			return
		}
		key := en.LibraryID.String() + "|" + en.PosterPath
		if cid, ok := byPath[key]; ok {
			en.ContentID = cid
			return
		}
		data, err := s.d.Posters.Poster(ctx, en.LibraryID, en.PosterPath)
		if err != nil || len(data) == 0 {
			return
		}
		cid := fmt.Sprintf("poster-%d@onscreen", len(images)+1)
		images = append(images, email.InlineImage{ContentID: cid, ContentType: "image/jpeg", Data: data})
		byPath[key] = cid
		en.ContentID = cid
	}
	for si := range c.Sections {
		for ei := range c.Sections[si].Entries {
			attach(&c.Sections[si].Entries[ei])
		}
	}
	for i := range c.ContinueWatching {
		attach(&c.ContinueWatching[i])
	}
	return images
}
