package v1

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/mediastore"
)

func newStorageHandler(svc *mockSettingsService) *SettingsHandler {
	return NewSettingsHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestGetStorage_MasksSecretKey(t *testing.T) {
	svc := &mockSettingsService{storage: settings.StorageConfig{
		Enabled: true, Backend: "s3", Bucket: "media", SecretKey: "super-secret",
	}}
	h := newStorageHandler(svc)

	rec := httptest.NewRecorder()
	h.GetStorage(rec, httptest.NewRequest(http.MethodGet, "/settings/storage", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var dto storageSettingDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.SecretKey != secretMask {
		t.Errorf("SecretKey = %q, want mask %q (secret leaked?)", dto.SecretKey, secretMask)
	}
	if strings.Contains(rec.Body.String(), "super-secret") {
		t.Error("raw secret leaked into response body")
	}
}

func TestUpdateStorage_MaskPreservesStoredSecret_AndHotSwaps(t *testing.T) {
	svc := &mockSettingsService{storage: settings.StorageConfig{
		Enabled: true, Backend: "s3", Endpoint: "s3.example.com", Bucket: "media",
		SecretKey: "original-secret",
	}}
	provider := mediastore.NewProvider(mediastore.Local{})
	h := newStorageHandler(svc).SetMediaStoreProvider(provider)

	// Client edits the bucket but echoes the masked secret back unchanged. A CDN
	// base makes the post-swap SignedURL deterministic (no creds/network needed).
	body := `{"enabled":true,"backend":"s3","endpoint":"s3.example.com","bucket":"newbucket",` +
		`"secret_key":"****","use_ssl":true,"media_root":"/media","cdn_base_url":"https://cdn.example.com"}`
	rec := httptest.NewRecorder()
	h.UpdateStorage(rec, httptest.NewRequest(http.MethodPut, "/settings/storage", strings.NewReader(body)))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	if svc.storage.SecretKey != "original-secret" {
		t.Errorf("secret = %q, want preserved 'original-secret' (mask overwrote it)", svc.storage.SecretKey)
	}
	if svc.storage.Bucket != "newbucket" {
		t.Errorf("bucket = %q, want newbucket", svc.storage.Bucket)
	}
	// Provider should now hold the S3 backend (not Local): its SignedURL returns a
	// CDN URL rather than "" (Local's "can't offload" answer).
	got, _ := provider.SignedURL(httptest.NewRequest(http.MethodGet, "/", nil).Context(), "/media/x.mkv", 60)
	if !strings.HasPrefix(got, "https://cdn.example.com/") {
		t.Errorf("SignedURL = %q, want CDN URL — hot-swap to S3 didn't apply", got)
	}
}

func TestUpdateStorage_DisabledSwapsToLocal(t *testing.T) {
	svc := &mockSettingsService{}
	provider := mediastore.NewProvider(nil)
	h := newStorageHandler(svc).SetMediaStoreProvider(provider)

	body := `{"enabled":false,"backend":"local"}`
	rec := httptest.NewRecorder()
	h.UpdateStorage(rec, httptest.NewRequest(http.MethodPut, "/settings/storage", strings.NewReader(body)))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	// Local can't offload → SignedURL "".
	if got, _ := provider.SignedURL(httptest.NewRequest(http.MethodGet, "/", nil).Context(), "/x.mkv", 60); got != "" {
		t.Errorf("got %q, want \"\" (disabled → Local)", got)
	}
}

func TestBuildMediaStore(t *testing.T) {
	t.Run("disabled → Local, no remap", func(t *testing.T) {
		s, err := BuildMediaStore(settings.StorageConfig{Enabled: false})
		if err != nil {
			t.Fatal(err)
		}
		l, ok := s.(mediastore.Local)
		if !ok {
			t.Fatalf("got %T, want Local", s)
		}
		if len(l.Remap) != 0 {
			t.Errorf("unexpected remap: %v", l.Remap)
		}
	})

	t.Run("local backend + path mappings → Local with remap", func(t *testing.T) {
		s, err := BuildMediaStore(settings.StorageConfig{
			PathMappings: map[string]string{"/primary/media": "/standby/media"},
		})
		if err != nil {
			t.Fatal(err)
		}
		l, ok := s.(mediastore.Local)
		if !ok {
			t.Fatalf("got %T, want Local", s)
		}
		if len(l.Remap) != 1 || l.Remap[0].From != "/primary/media" || l.Remap[0].To != "/standby/media" {
			t.Errorf("remap not applied: %+v", l.Remap)
		}
	})

	t.Run("s3 backend → *S3", func(t *testing.T) {
		s, err := BuildMediaStore(settings.StorageConfig{
			Enabled: true, Backend: "s3", Endpoint: "s3.example.com", Bucket: "media",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := s.(*mediastore.S3); !ok {
			t.Errorf("got %T, want *S3", s)
		}
	})

	t.Run("s3 backend missing bucket → error", func(t *testing.T) {
		if _, err := BuildMediaStore(settings.StorageConfig{Enabled: true, Backend: "s3", Endpoint: "s3.example.com"}); err == nil {
			t.Error("expected error for s3 config without a bucket")
		}
	})
}

func TestPathMappingsToRemap(t *testing.T) {
	if pathMappingsToRemap(nil) != nil {
		t.Error("nil map should yield nil")
	}
	if pathMappingsToRemap(map[string]string{}) != nil {
		t.Error("empty map should yield nil")
	}
	// Longest prefix first, so the most specific mapping wins at match time.
	got := pathMappingsToRemap(map[string]string{"/a": "/x", "/a/b/c": "/y", "/a/b": "/z"})
	if len(got) != 3 {
		t.Fatalf("got %d mappings, want 3", len(got))
	}
	if got[0].From != "/a/b/c" || got[len(got)-1].From != "/a" {
		t.Errorf("not longest-first: %+v", got)
	}
}

func TestStorageDTO_RoundTrip(t *testing.T) {
	cfg := settings.StorageConfig{
		Enabled: true, Backend: "s3", Endpoint: "s3.example.com", Bucket: "b",
		AccessKey: "ak", SecretKey: "real-secret", UseSSL: true,
		MediaRoot: "/m", PathPrefix: "p/", CDNBaseURL: "https://cdn",
		PathMappings: map[string]string{"/a": "/b"},
	}
	dto := toStorageDTO(cfg)
	if dto.SecretKey != secretMask {
		t.Errorf("secret not masked: %q", dto.SecretKey)
	}

	// Echoing the mask back preserves the stored secret; everything else applies.
	back := storageFromDTO(dto, cfg)
	if back.SecretKey != "real-secret" {
		t.Errorf("mask did not preserve secret: %q", back.SecretKey)
	}
	if back.Bucket != "b" || back.MediaRoot != "/m" || back.CDNBaseURL != "https://cdn" {
		t.Errorf("fields lost: %+v", back)
	}
	if back.PathMappings["/a"] != "/b" {
		t.Errorf("path mappings lost: %+v", back.PathMappings)
	}

	// A real new secret replaces the stored one.
	dto.SecretKey = "new-secret"
	if got := storageFromDTO(dto, cfg).SecretKey; got != "new-secret" {
		t.Errorf("new secret not applied: %q", got)
	}
}

func TestTestStorage_LocalBackendReportsOK(t *testing.T) {
	svc := &mockSettingsService{}
	h := newStorageHandler(svc)

	body := `{"enabled":false,"backend":"local"}`
	rec := httptest.NewRecorder()
	h.TestStorage(rec, httptest.NewRequest(http.MethodPost, "/settings/storage/test", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var res storageTestResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Errorf("local test: ok=false (err=%q), want ok=true", res.Error)
	}
}
