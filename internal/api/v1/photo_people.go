package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// PhotoPeopleDB is the slice of generated queries behind the photo-library
// extras of migration 00057: photo settings, faces and people, memories.
type PhotoPeopleDB interface {
	GetLibrary(ctx context.Context, id uuid.UUID) (gen.Library, error)
	GetMediaItem(ctx context.Context, id uuid.UUID) (gen.GetMediaItemRow, error)

	GetLibraryPhotoSettings(ctx context.Context, libraryID uuid.UUID) (gen.LibraryPhotoSetting, error)
	UpsertLibraryPhotoSettings(ctx context.Context, arg gen.UpsertLibraryPhotoSettingsParams) (gen.LibraryPhotoSetting, error)

	ListPhotoFacesForItem(ctx context.Context, itemID uuid.UUID) ([]gen.ListPhotoFacesForItemRow, error)
	GetPhotoFace(ctx context.Context, id uuid.UUID) (gen.PhotoFace, error)
	SetPhotoFacePerson(ctx context.Context, arg gen.SetPhotoFacePersonParams) error
	DeletePhotoFace(ctx context.Context, id uuid.UUID) error
	ListUnnamedPhotoFaces(ctx context.Context, arg gen.ListUnnamedPhotoFacesParams) ([]gen.ListUnnamedPhotoFacesRow, error)

	GetPhotoPerson(ctx context.Context, id uuid.UUID) (gen.PhotoPerson, error)
	FindOrCreatePhotoPerson(ctx context.Context, arg gen.FindOrCreatePhotoPersonParams) (gen.PhotoPerson, error)
	RenamePhotoPerson(ctx context.Context, arg gen.RenamePhotoPersonParams) (gen.PhotoPerson, error)
	DeletePhotoPerson(ctx context.Context, id uuid.UUID) error
	ListPhotoPeople(ctx context.Context, arg gen.ListPhotoPeopleParams) ([]gen.ListPhotoPeopleRow, error)
	ListPhotoPersonItems(ctx context.Context, arg gen.ListPhotoPersonItemsParams) ([]gen.ListPhotoPersonItemsRow, error)
	CountPhotoPersonItems(ctx context.Context, arg gen.CountPhotoPersonItemsParams) (int64, error)
	ListPhotoPersonSuggestions(ctx context.Context, arg gen.ListPhotoPersonSuggestionsParams) ([]gen.ListPhotoPersonSuggestionsRow, error)

	DeletePhotoFacesForLibrary(ctx context.Context, libraryID pgtype.UUID) (int64, error)
	DeletePhotoPeopleForLibrary(ctx context.Context, libraryID pgtype.UUID) (int64, error)
	DeletePhotoFaceScansForLibrary(ctx context.Context, libraryID pgtype.UUID) (int64, error)

	ListPhotoLibraryIDs(ctx context.Context) ([]uuid.UUID, error)
	ListPhotoMemoryCandidates(ctx context.Context, arg gen.ListPhotoMemoryCandidatesParams) ([]gen.ListPhotoMemoryCandidatesRow, error)
	CountDatedPhotos(ctx context.Context, libraryIds []uuid.UUID) (int64, error)
}

// PhotoPeopleHandler serves photo-library settings, faces and people, and
// memories.
//
// Visibility: face data belongs to its photo. Every face, person and memory
// endpoint applies the library ACL, the content-rating ceiling and the label
// restriction of the photos it reveals, and a person whose photos are all
// hidden from the caller doesn't exist for them (404), so a name can't leak
// through a restricted profile.
//
// Naming: admins, and users with access to the library who carry no rating
// ceiling or label restriction (the household's own accounts, as opposed to
// managed profiles), may name faces and edit people. Photo items have no
// per-photo owner, so library access is the ownership test.
type PhotoPeopleHandler struct {
	db     PhotoPeopleDB
	access LibraryAccessChecker
	audit  *audit.Logger
	logger *slog.Logger
	now    func() time.Time
}

// NewPhotoPeopleHandler wires the handler.
func NewPhotoPeopleHandler(db PhotoPeopleDB, logger *slog.Logger) *PhotoPeopleHandler {
	return &PhotoPeopleHandler{db: db, logger: logger, now: time.Now}
}

// WithLibraryAccess attaches the per-library ACL checker.
func (h *PhotoPeopleHandler) WithLibraryAccess(a LibraryAccessChecker) *PhotoPeopleHandler {
	h.access = a
	return h
}

// LibraryAccessWired reports whether the library-ACL checker is wired (a nil
// checker fails open; see api.Handlers.ValidateLibraryAccess).
func (h *PhotoPeopleHandler) LibraryAccessWired() bool { return h.access != nil }

// WithAudit attaches an audit logger.
func (h *PhotoPeopleHandler) WithAudit(a *audit.Logger) *PhotoPeopleHandler {
	h.audit = a
	return h
}

// ── settings ─────────────────────────────────────────────────────────────

type photoSettingsJSON struct {
	LibraryID      string `json:"library_id"`
	RawJPEGPairing bool   `json:"raw_jpeg_pairing"`
}

// photoLibrary loads a library and 404s unless it is a photo library.
func (h *PhotoPeopleHandler) photoLibrary(w http.ResponseWriter, r *http.Request, id uuid.UUID) bool {
	lib, err := h.db.GetLibrary(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return false
		}
		h.logger.ErrorContext(r.Context(), "get library", "library_id", id, "err", err)
		respond.InternalError(w, r)
		return false
	}
	if lib.Type != "photo" || lib.DeletedAt.Valid {
		respond.NotFound(w, r)
		return false
	}
	return true
}

// GetSettings handles GET /api/v1/libraries/{id}/photo-settings (admin).
func (h *PhotoPeopleHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return
	}
	if !h.photoLibrary(w, r, id) {
		return
	}
	out := photoSettingsJSON{LibraryID: id.String(), RawJPEGPairing: true}
	row, err := h.db.GetLibraryPhotoSettings(r.Context(), id)
	switch {
	case err == nil:
		out.RawJPEGPairing = row.RawJpegPairing
	case errors.Is(err, pgx.ErrNoRows):
		// Defaults.
	default:
		h.logger.ErrorContext(r.Context(), "get photo settings", "library_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, out)
}

// UpdateSettings handles PUT /api/v1/libraries/{id}/photo-settings (admin).
// A pairing change regroups the library's RAW + JPEG pairs on its next scan.
func (h *PhotoPeopleHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return
	}
	var body struct {
		RawJPEGPairing *bool `json:"raw_jpeg_pairing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RawJPEGPairing == nil {
		respond.BadRequest(w, r, "raw_jpeg_pairing is required")
		return
	}
	if !h.photoLibrary(w, r, id) {
		return
	}
	row, err := h.db.UpsertLibraryPhotoSettings(r.Context(), gen.UpsertLibraryPhotoSettingsParams{
		LibraryID: id, RawJpegPairing: *body.RawJPEGPairing,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "save photo settings", "library_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
			h.audit.Log(r.Context(), &claims.UserID, audit.ActionSettingsUpdate, id.String(), map[string]any{
				"library_photo_settings": map[string]any{"raw_jpeg_pairing": row.RawJpegPairing},
			}, audit.ClientIP(r))
		}
	}
	respond.Success(w, r, photoSettingsJSON{LibraryID: id.String(), RawJPEGPairing: row.RawJpegPairing})
}

// ── shared checks ────────────────────────────────────────────────────────

// libraryVisible applies the library ACL (404 on denial).
func (h *PhotoPeopleHandler) libraryVisible(w http.ResponseWriter, r *http.Request, libraryID uuid.UUID) bool {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return false
	}
	if h.access == nil {
		return true
	}
	ok, err := h.access.CanAccessLibrary(r.Context(), claims.UserID, libraryID, claims.IsAdmin)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "check library access", "library_id", libraryID, "err", err)
		respond.InternalError(w, r)
		return false
	}
	if !ok {
		respond.NotFound(w, r)
		return false
	}
	return true
}

// photoVisible loads a photo item and applies the library ACL, rating
// ceiling and label restriction (404 on any denial).
func (h *PhotoPeopleHandler) photoVisible(w http.ResponseWriter, r *http.Request, itemID uuid.UUID) (gen.GetMediaItemRow, bool) {
	item, err := h.db.GetMediaItem(r.Context(), itemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return item, false
		}
		h.logger.ErrorContext(r.Context(), "get photo", "id", itemID, "err", err)
		respond.InternalError(w, r)
		return item, false
	}
	if item.Type != "photo" || item.DeletedAt.Valid {
		respond.NotFound(w, r)
		return item, false
	}
	if !itemAddAllowed(w, r, h.access, h.logger, item) {
		return item, false
	}
	return item, true
}

// canEditFaces reports whether the caller may name faces and edit people
// (see PhotoPeopleHandler). Library access is checked by the caller.
func canEditFaces(r *http.Request) bool {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return false
	}
	return claims.IsAdmin || !viewerRestricted(claims)
}

// listFilter is the caller's rating ceiling + label restriction, as the
// generated queries take them.
type listFilter struct {
	rank             *int32
	blocked, allowed []string
}

func callerListFilter(r *http.Request) listFilter {
	claims := middleware.ClaimsFromContext(r.Context())
	f := listFilter{}
	if claims != nil {
		f.rank = maxRatingRankFromClaims(claims.MaxContentRating)
	}
	l := callerLabels(r)
	f.blocked, f.allowed = l.BlockedArg(), l.AllowedArg()
	return f
}

// maxPersonNameLen bounds a person's name, in runes (matches the column's
// CHECK).
const maxPersonNameLen = 100

// normalizePersonName collapses whitespace and validates a person's name.
func normalizePersonName(s string) (string, error) {
	n := strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
	if n == "" {
		return "", errors.New("name is required")
	}
	if utf8.RuneCountInString(n) > maxPersonNameLen {
		return "", errors.New("name is longer than 100 characters")
	}
	for _, c := range n {
		if unicode.IsControl(c) {
			return "", errors.New("name contains a control character")
		}
	}
	return n, nil
}

func uuidPtrString(u pgtype.UUID) *string {
	if !u.Valid {
		return nil
	}
	s := uuid.UUID(u.Bytes).String()
	return &s
}

// ── faces ────────────────────────────────────────────────────────────────

// PhotoFaceResponse is one face on a photo. The box is normalised to 0..1 of
// the upright photo.
type PhotoFaceResponse struct {
	ID         string  `json:"id"`
	ItemID     string  `json:"item_id"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	W          float64 `json:"w"`
	H          float64 `json:"h"`
	Score      float64 `json:"score"`
	Source     string  `json:"source,omitempty"`
	PersonID   *string `json:"person_id,omitempty"`
	PersonName *string `json:"person_name,omitempty"`
}

// ItemFaces handles GET /api/v1/items/{id}/faces: the photo's face boxes and
// their names, plus whether the caller may name them.
func (h *PhotoPeopleHandler) ItemFaces(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	if _, ok := h.photoVisible(w, r, id); !ok {
		return
	}
	rows, err := h.db.ListPhotoFacesForItem(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list photo faces", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]PhotoFaceResponse, len(rows))
	for i, f := range rows {
		out[i] = PhotoFaceResponse{
			ID: f.ID.String(), ItemID: f.ItemID.String(),
			X: float64(f.X), Y: float64(f.Y), W: float64(f.W), H: float64(f.H),
			Score: float64(f.Score), Source: f.Source,
			PersonID: uuidPtrString(f.PersonID), PersonName: f.PersonName,
		}
	}
	respond.Success(w, r, map[string]any{"faces": out, "can_edit": canEditFaces(r)})
}

// faceForEdit loads a face, checks its photo is visible and the caller may
// edit it.
func (h *PhotoPeopleHandler) faceForEdit(w http.ResponseWriter, r *http.Request) (gen.PhotoFace, bool) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid face id")
		return gen.PhotoFace{}, false
	}
	face, err := h.db.GetPhotoFace(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return face, false
		}
		h.logger.ErrorContext(r.Context(), "get photo face", "id", id, "err", err)
		respond.InternalError(w, r)
		return face, false
	}
	if _, ok := h.photoVisible(w, r, face.ItemID); !ok {
		return face, false
	}
	if !canEditFaces(r) {
		respond.Forbidden(w, r)
		return face, false
	}
	return face, true
}

// UpdateFace handles PUT /api/v1/photos/faces/{id}: name a face.
//
//	{"name": "Alice"}       the library's person called Alice, created if new
//	{"person_id": "<uuid>"} an existing person of the same library
//	{"person_id": null} or {"name": ""}   unname the face
func (h *PhotoPeopleHandler) UpdateFace(w http.ResponseWriter, r *http.Request) {
	face, ok := h.faceForEdit(w, r)
	if !ok {
		return
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	ctx := r.Context()
	var person *gen.PhotoPerson
	switch {
	case body["name"] != nil:
		var name *string
		if err := json.Unmarshal(body["name"], &name); err != nil {
			respond.BadRequest(w, r, "name must be a string")
			return
		}
		if name != nil && strings.TrimSpace(*name) != "" {
			n, err := normalizePersonName(*name)
			if err != nil {
				respond.ValidationError(w, r, err.Error())
				return
			}
			claims := middleware.ClaimsFromContext(ctx)
			p, err := h.db.FindOrCreatePhotoPerson(ctx, gen.FindOrCreatePhotoPersonParams{
				LibraryID: face.LibraryID, Name: n,
				CreatedBy: pgtype.UUID{Bytes: claims.UserID, Valid: true},
			})
			if err != nil {
				h.logger.ErrorContext(ctx, "find or create photo person", "err", err)
				respond.InternalError(w, r)
				return
			}
			person = &p
		}
	case body["person_id"] != nil:
		var pid *string
		if err := json.Unmarshal(body["person_id"], &pid); err != nil {
			respond.BadRequest(w, r, "person_id must be a string or null")
			return
		}
		if pid != nil {
			id, err := uuid.Parse(*pid)
			if err != nil {
				respond.BadRequest(w, r, "invalid person_id")
				return
			}
			p, err := h.db.GetPhotoPerson(ctx, id)
			if err != nil || p.LibraryID != face.LibraryID {
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					h.logger.ErrorContext(ctx, "get photo person", "id", id, "err", err)
					respond.InternalError(w, r)
					return
				}
				respond.ValidationError(w, r, "person_id is not a person in this library")
				return
			}
			person = &p
		}
	default:
		respond.BadRequest(w, r, "name or person_id is required")
		return
	}

	arg := gen.SetPhotoFacePersonParams{ID: face.ID}
	if person != nil {
		arg.PersonID = pgtype.UUID{Bytes: person.ID, Valid: true}
	}
	if err := h.db.SetPhotoFacePerson(ctx, arg); err != nil {
		h.logger.ErrorContext(ctx, "set photo face person", "face_id", face.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := PhotoFaceResponse{
		ID: face.ID.String(), ItemID: face.ItemID.String(),
		X: float64(face.X), Y: float64(face.Y), W: float64(face.W), H: float64(face.H),
		Score: float64(face.Score), Source: face.Source,
	}
	if person != nil {
		pid := person.ID.String()
		out.PersonID, out.PersonName = &pid, &person.Name
	}
	respond.Success(w, r, out)
}

// DeleteFace handles DELETE /api/v1/photos/faces/{id}: drop a box that isn't
// a face.
func (h *PhotoPeopleHandler) DeleteFace(w http.ResponseWriter, r *http.Request) {
	face, ok := h.faceForEdit(w, r)
	if !ok {
		return
	}
	if err := h.db.DeletePhotoFace(r.Context(), face.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "delete photo face", "face_id", face.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// PhotoFaceThumbResponse is a face to show cropped from its photo.
type PhotoFaceThumbResponse struct {
	ID     string  `json:"id"`
	ItemID string  `json:"item_id"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	W      float64 `json:"w"`
	H      float64 `json:"h"`
	Score  float64 `json:"score"`
}

// UnnamedFaces handles GET /api/v1/photos/faces?library_id=&limit=&offset=:
// faces nobody has named yet, on photos the caller may see.
func (h *PhotoPeopleHandler) UnnamedFaces(w http.ResponseWriter, r *http.Request) {
	libID, err := uuid.Parse(r.URL.Query().Get("library_id"))
	if err != nil {
		respond.BadRequest(w, r, "library_id is required")
		return
	}
	if !h.libraryVisible(w, r, libID) {
		return
	}
	limit := parseInt32(r.URL.Query().Get("limit"), 60)
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	offset := parseInt32(r.URL.Query().Get("offset"), 0)
	f := callerListFilter(r)
	rows, err := h.db.ListUnnamedPhotoFaces(r.Context(), gen.ListUnnamedPhotoFacesParams{
		LibraryID: libID, Limit: limit, Offset: offset,
		MaxRatingRank: f.rank, BlockedLabels: f.blocked, AllowedLabels: f.allowed,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list unnamed faces", "library_id", libID, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]PhotoFaceThumbResponse, len(rows))
	for i, f := range rows {
		out[i] = PhotoFaceThumbResponse{ID: f.ID.String(), ItemID: f.ItemID.String(),
			X: float64(f.X), Y: float64(f.Y), W: float64(f.W), H: float64(f.H), Score: float64(f.Score)}
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// DeleteFaceData handles DELETE /api/v1/admin/photos/face-data[?library_id=]
// (admin): every face, person and detection record, in one library or all.
// With the face-detection task still enabled, photos are detected again on
// its next run; disable the task to keep face data off.
func (h *PhotoPeopleHandler) DeleteFaceData(w http.ResponseWriter, r *http.Request) {
	var lib pgtype.UUID
	scope := "all"
	if s := r.URL.Query().Get("library_id"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			respond.BadRequest(w, r, "invalid library_id")
			return
		}
		lib = pgtype.UUID{Bytes: id, Valid: true}
		scope = id.String()
	}
	ctx := r.Context()
	nFaces, err := h.db.DeletePhotoFacesForLibrary(ctx, lib)
	if err == nil {
		var nPeople, nScans int64
		nPeople, err = h.db.DeletePhotoPeopleForLibrary(ctx, lib)
		if err == nil {
			nScans, err = h.db.DeletePhotoFaceScansForLibrary(ctx, lib)
		}
		if err == nil {
			if h.audit != nil {
				if claims := middleware.ClaimsFromContext(ctx); claims != nil {
					h.audit.Log(ctx, &claims.UserID, audit.ActionPhotoFaceDataDelete, scope, map[string]any{
						"faces": nFaces, "people": nPeople, "scans": nScans,
					}, audit.ClientIP(r))
				}
			}
			respond.Success(w, r, map[string]int64{"faces": nFaces, "people": nPeople, "scans": nScans})
			return
		}
	}
	h.logger.ErrorContext(ctx, "delete face data", "scope", scope, "err", err)
	respond.InternalError(w, r)
}

// ── people ───────────────────────────────────────────────────────────────

// PhotoPersonResponse is one person of a photo library.
type PhotoPersonResponse struct {
	ID         string                  `json:"id"`
	LibraryID  string                  `json:"library_id"`
	Name       string                  `json:"name"`
	PhotoCount int64                   `json:"photo_count"`
	Cover      *PhotoFaceThumbResponse `json:"cover,omitempty"`
	CanEdit    bool                    `json:"can_edit"`
}

// People handles GET /api/v1/photos/people?library_id=: the library's
// people with at least one photo the caller may see, most photos first.
func (h *PhotoPeopleHandler) People(w http.ResponseWriter, r *http.Request) {
	libID, err := uuid.Parse(r.URL.Query().Get("library_id"))
	if err != nil {
		respond.BadRequest(w, r, "library_id is required")
		return
	}
	if !h.libraryVisible(w, r, libID) {
		return
	}
	f := callerListFilter(r)
	rows, err := h.db.ListPhotoPeople(r.Context(), gen.ListPhotoPeopleParams{
		LibraryID: libID, MaxRatingRank: f.rank, BlockedLabels: f.blocked, AllowedLabels: f.allowed,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list photo people", "library_id", libID, "err", err)
		respond.InternalError(w, r)
		return
	}
	edit := canEditFaces(r)
	out := make([]PhotoPersonResponse, len(rows))
	for i, p := range rows {
		out[i] = PhotoPersonResponse{
			ID: p.ID.String(), LibraryID: libID.String(), Name: p.Name, PhotoCount: p.PhotoCount, CanEdit: edit,
			Cover: &PhotoFaceThumbResponse{ID: p.CoverFaceID.String(), ItemID: p.CoverItemID.String(),
				X: float64(p.CoverX), Y: float64(p.CoverY), W: float64(p.CoverW), H: float64(p.CoverH)},
		}
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// personVisible loads a person and checks the caller may see them: library
// access, and at least one visible photo (editors also see a person whose
// faces were all unnamed, to rename or delete them).
func (h *PhotoPeopleHandler) personVisible(w http.ResponseWriter, r *http.Request) (gen.PhotoPerson, int64, bool) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid person id")
		return gen.PhotoPerson{}, 0, false
	}
	p, err := h.db.GetPhotoPerson(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return p, 0, false
		}
		h.logger.ErrorContext(r.Context(), "get photo person", "id", id, "err", err)
		respond.InternalError(w, r)
		return p, 0, false
	}
	if !h.libraryVisible(w, r, p.LibraryID) {
		return p, 0, false
	}
	f := callerListFilter(r)
	n, err := h.db.CountPhotoPersonItems(r.Context(), gen.CountPhotoPersonItemsParams{
		PersonID: p.ID, MaxRatingRank: f.rank, BlockedLabels: f.blocked, AllowedLabels: f.allowed,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "count person photos", "id", id, "err", err)
		respond.InternalError(w, r)
		return p, 0, false
	}
	if n == 0 && !canEditFaces(r) {
		respond.NotFound(w, r)
		return p, 0, false
	}
	return p, n, true
}

// Person handles GET /api/v1/photos/people/{id}.
func (h *PhotoPeopleHandler) Person(w http.ResponseWriter, r *http.Request) {
	p, n, ok := h.personVisible(w, r)
	if !ok {
		return
	}
	respond.Success(w, r, PhotoPersonResponse{
		ID: p.ID.String(), LibraryID: p.LibraryID.String(), Name: p.Name, PhotoCount: n, CanEdit: canEditFaces(r),
	})
}

// PersonItems handles GET /api/v1/photos/people/{id}/items?limit=&offset=:
// the person's photos the caller may see, newest first.
func (h *PhotoPeopleHandler) PersonItems(w http.ResponseWriter, r *http.Request) {
	p, n, ok := h.personVisible(w, r)
	if !ok {
		return
	}
	limit := parseInt32(r.URL.Query().Get("limit"), 100)
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset := parseInt32(r.URL.Query().Get("offset"), 0)
	f := callerListFilter(r)
	rows, err := h.db.ListPhotoPersonItems(r.Context(), gen.ListPhotoPersonItemsParams{
		PersonID: p.ID, Limit: limit, Offset: offset,
		MaxRatingRank: f.rank, BlockedLabels: f.blocked, AllowedLabels: f.allowed,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list person photos", "id", p.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]PhotoListItemResponse, len(rows))
	for i, row := range rows {
		out[i] = PhotoListItemResponse{
			ID: row.ID, LibraryID: row.LibraryID, Title: row.Title, PosterPath: row.PosterPath,
			TakenAt: timestamptzPtr(row.TakenAt), Width: row.Width, Height: row.Height,
			Orientation: row.Orientation, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		}
	}
	respond.List(w, r, out, n, "")
}

// PersonSuggestions handles GET /api/v1/photos/people/{id}/suggestions:
// unnamed faces likely to be this person (see ListPhotoPersonSuggestions —
// taken around the same time as their named photos). Matching stays manual;
// this only narrows the search.
func (h *PhotoPeopleHandler) PersonSuggestions(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.personVisible(w, r)
	if !ok {
		return
	}
	limit := parseInt32(r.URL.Query().Get("limit"), 24)
	if limit <= 0 || limit > 100 {
		limit = 24
	}
	f := callerListFilter(r)
	rows, err := h.db.ListPhotoPersonSuggestions(r.Context(), gen.ListPhotoPersonSuggestionsParams{
		LibraryID: p.LibraryID, PersonID: p.ID, Lim: limit,
		MaxRatingRank: f.rank, BlockedLabels: f.blocked, AllowedLabels: f.allowed,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "person suggestions", "id", p.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]PhotoFaceThumbResponse, len(rows))
	for i, f := range rows {
		out[i] = PhotoFaceThumbResponse{ID: f.ID.String(), ItemID: f.ItemID.String(),
			X: float64(f.X), Y: float64(f.Y), W: float64(f.W), H: float64(f.H), Score: float64(f.Score)}
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// UpdatePerson handles PATCH /api/v1/photos/people/{id} {"name": "..."}.
func (h *PhotoPeopleHandler) UpdatePerson(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.personVisible(w, r)
	if !ok {
		return
	}
	if !canEditFaces(r) {
		respond.Forbidden(w, r)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	name, err := normalizePersonName(body.Name)
	if err != nil {
		respond.ValidationError(w, r, err.Error())
		return
	}
	updated, err := h.db.RenamePhotoPerson(r.Context(), gen.RenamePhotoPersonParams{ID: p.ID, Name: name})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			respond.Error(w, r, http.StatusConflict, "CONFLICT", "another person in this library already has this name")
			return
		}
		h.logger.ErrorContext(r.Context(), "rename photo person", "id", p.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, PhotoPersonResponse{
		ID: updated.ID.String(), LibraryID: updated.LibraryID.String(), Name: updated.Name, CanEdit: true,
	})
}

// DeletePerson handles DELETE /api/v1/photos/people/{id}. Their faces stay,
// unnamed.
func (h *PhotoPeopleHandler) DeletePerson(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.personVisible(w, r)
	if !ok {
		return
	}
	if !canEditFaces(r) {
		respond.Forbidden(w, r)
		return
	}
	if err := h.db.DeletePhotoPerson(r.Context(), p.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "delete photo person", "id", p.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

func timestamptzPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
