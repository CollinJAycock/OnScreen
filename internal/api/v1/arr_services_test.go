package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/arrcrypt"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// recordingArrDB captures the params the handler hands the data layer, so a
// test can assert on exactly what would have been written to the column.
type recordingArrDB struct {
	created gen.CreateArrServiceParams
	updated gen.UpdateArrServiceParams
	stored  gen.ArrService
}

func (d *recordingArrDB) ListArrServices(_ context.Context) ([]gen.ArrService, error) {
	return nil, nil
}
func (d *recordingArrDB) GetArrService(_ context.Context, _ uuid.UUID) (gen.ArrService, error) {
	return d.stored, nil
}
func (d *recordingArrDB) CreateArrService(_ context.Context, p gen.CreateArrServiceParams) (gen.ArrService, error) {
	d.created = p
	return gen.ArrService{ID: p.ID, Name: p.Name, Kind: p.Kind, BaseUrl: p.BaseUrl, ApiKey: p.ApiKey}, nil
}
func (d *recordingArrDB) UpdateArrService(_ context.Context, p gen.UpdateArrServiceParams) (gen.ArrService, error) {
	d.updated = p
	return gen.ArrService{ID: p.ID, Name: p.Name, BaseUrl: p.BaseUrl, ApiKey: p.ApiKey}, nil
}
func (d *recordingArrDB) DeleteArrService(_ context.Context, _ uuid.UUID) error { return nil }
func (d *recordingArrDB) SetArrServiceDefault(_ context.Context, _ uuid.UUID) error {
	return nil
}
func (d *recordingArrDB) ClearArrServiceDefault(_ context.Context, _ string) error { return nil }

func arrTestEncryptor(t *testing.T) *auth.Encryptor {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 3)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	return enc
}

// The credential must be sealed before it reaches the column. A database dump,
// a backup, or a read-only SQL injection otherwise yields a working Sonarr or
// Radarr key, which carries full control of the downloader.
func TestArrServices_Create_SealsAPIKeyBeforeStoring(t *testing.T) {
	enc := arrTestEncryptor(t)
	db := &recordingArrDB{}
	h := NewArrServicesHandler(db, slog.Default()).WithEncryptor(enc)

	body, err := json.Marshal(map[string]any{
		"name": "Main Sonarr", "kind": "sonarr",
		"base_url": "http://sonarr.lan:8989", "api_key": "s3cr3t-sonarr-key",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest("POST", "/", strings.NewReader(string(body))))

	if rec.Code != 201 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if db.created.ApiKey == "s3cr3t-sonarr-key" {
		t.Fatal("api_key was written to the column in cleartext")
	}
	if !arrcrypt.IsSealed(db.created.ApiKey) {
		t.Fatalf("stored api_key is not sealed: %q", db.created.ApiKey)
	}
	if db.created.ID == uuid.Nil {
		t.Fatal("no row id was supplied; the ciphertext could not be bound to its row")
	}

	// And it must open again — a write path that seals unreadably is worse
	// than one that stores cleartext.
	got, err := arrcrypt.Open(enc, db.created.ID, db.created.ApiKey)
	if err != nil {
		t.Fatalf("open stored key: %v", err)
	}
	if got != "s3cr3t-sonarr-key" {
		t.Errorf("round-trip: got %q, want the original key", got)
	}

	// The response must not echo the credential back either.
	if strings.Contains(rec.Body.String(), "s3cr3t-sonarr-key") {
		t.Error("response body contains the api key")
	}
}

// Rotating the key through Update must seal too, bound to the row it belongs
// to — the id here comes from the URL, not from a freshly minted UUID.
func TestArrServices_Update_SealsReplacementAPIKey(t *testing.T) {
	enc := arrTestEncryptor(t)
	id := uuid.New()
	db := &recordingArrDB{stored: gen.ArrService{
		ID: id, Name: "Main Sonarr", Kind: "sonarr", BaseUrl: "http://sonarr.lan:8989",
	}}
	h := NewArrServicesHandler(db, slog.Default()).WithEncryptor(enc)

	body, err := json.Marshal(map[string]any{"api_key": "rotated-key"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := withChiParam(
		httptest.NewRequest("PATCH", "/", strings.NewReader(string(body))), "id", id.String())
	rec := httptest.NewRecorder()
	h.Update(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if db.updated.ApiKey == "rotated-key" {
		t.Fatal("replacement api_key was written in cleartext")
	}
	got, err := arrcrypt.Open(enc, id, db.updated.ApiKey)
	if err != nil {
		t.Fatalf("open updated key: %v", err)
	}
	if got != "rotated-key" {
		t.Errorf("round-trip: got %q, want %q", got, "rotated-key")
	}
}

// An Update that doesn't touch the key must carry the stored ciphertext
// through untouched — re-sealing an already-sealed value would double-encrypt
// it, and treating it as plaintext would too.
func TestArrServices_Update_LeavesUntouchedKeySealedOnce(t *testing.T) {
	enc := arrTestEncryptor(t)
	id := uuid.New()
	sealed, err := arrcrypt.Seal(enc, id, "original-key")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	db := &recordingArrDB{stored: gen.ArrService{
		ID: id, Name: "Main Sonarr", Kind: "sonarr",
		BaseUrl: "http://sonarr.lan:8989", ApiKey: sealed,
	}}
	h := NewArrServicesHandler(db, slog.Default()).WithEncryptor(enc)

	body, err := json.Marshal(map[string]any{"name": "Renamed"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := withChiParam(
		httptest.NewRequest("PATCH", "/", strings.NewReader(string(body))), "id", id.String())
	rec := httptest.NewRecorder()
	h.Update(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if db.updated.ApiKey != sealed {
		t.Errorf("stored key changed on an unrelated edit:\n got %q\nwant %q",
			db.updated.ApiKey, sealed)
	}
	got, err := arrcrypt.Open(enc, id, db.updated.ApiKey)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got != "original-key" {
		t.Errorf("round-trip after unrelated edit: got %q, want %q", got, "original-key")
	}
}

// With no encryptor wired the handler must behave exactly as before, so
// enabling at-rest encryption is opt-in and an un-migrated deployment keeps
// working.
func TestArrServices_Create_NoEncryptorStoresCleartext(t *testing.T) {
	db := &recordingArrDB{}
	h := NewArrServicesHandler(db, slog.Default())

	body, err := json.Marshal(map[string]any{
		"name": "Radarr", "kind": "radarr",
		"base_url": "http://radarr.lan:7878", "api_key": "plain-key",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest("POST", "/", strings.NewReader(string(body))))

	if rec.Code != 201 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if db.created.ApiKey != "plain-key" {
		t.Errorf("got %q, want the key stored as before", db.created.ApiKey)
	}
}
