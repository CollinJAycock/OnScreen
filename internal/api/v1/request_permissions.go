package v1

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// jsonField is a JSON body field that records whether it was present and
// whether it was an explicit null, which a plain pointer can't tell apart
// ("omitted" and null both decode to nil). encoding/json calls UnmarshalJSON
// for a present key even when its value is null, and never for an absent one.
type jsonField[T any] struct {
	Set   bool // the key was in the body
	Null  bool // ...with the value null
	Value T
}

func (f *jsonField[T]) UnmarshalJSON(b []byte) error {
	f.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		f.Null = true
		return nil
	}
	return json.Unmarshal(b, &f.Value)
}

// requestPermissionsBody is PUT /users/{id}/request-permissions. Every field
// is optional; see UserHandler.SetRequestPermissions.
type requestPermissionsBody struct {
	AutoApproveMovies jsonField[bool]  `json:"auto_approve_movies"`
	AutoApproveTV     jsonField[bool]  `json:"auto_approve_tv"`
	CanRequest        jsonField[bool]  `json:"can_request"`
	QuotaMovies       jsonField[int32] `json:"quota_movies"`
	QuotaTV           jsonField[int32] `json:"quota_tv"`
}

// params validates the body and builds the partial update plus the audit
// detail (only the fields being changed). A non-empty message means 422.
func (b requestPermissionsBody) params(id uuid.UUID) (gen.SetUserRequestPermissionsParams, map[string]any, string) {
	p := gen.SetUserRequestPermissionsParams{ID: id}
	detail := map[string]any{}

	toggles := []struct {
		name string
		f    jsonField[bool]
		dst  **bool
	}{
		{"auto_approve_movies", b.AutoApproveMovies, &p.AutoApproveMovies},
		{"auto_approve_tv", b.AutoApproveTV, &p.AutoApproveTv},
		{"can_request", b.CanRequest, &p.CanRequest},
	}
	for _, t := range toggles {
		if !t.f.Set {
			continue
		}
		if t.f.Null {
			return p, nil, t.name + " must be true or false"
		}
		v := t.f.Value
		*t.dst = &v
		detail[t.name] = v
	}

	quotas := []struct {
		name string
		f    jsonField[int32]
		set  *bool
		dst  **int32
	}{
		{"quota_movies", b.QuotaMovies, &p.SetQuotaMovies, &p.RequestQuotaMovies},
		{"quota_tv", b.QuotaTV, &p.SetQuotaTv, &p.RequestQuotaTv},
	}
	for _, q := range quotas {
		if !q.f.Set {
			continue
		}
		*q.set = true
		if q.f.Null {
			// Back to the server default.
			detail[q.name] = nil
			continue
		}
		if q.f.Value < 0 || q.f.Value > settings.MaxRequestQuota {
			return p, nil, fmt.Sprintf("%s must be null (server default), 0 (unlimited) or 1-%d", q.name, settings.MaxRequestQuota)
		}
		v := q.f.Value
		*q.dst = &v
		detail[q.name] = v
	}

	if len(detail) == 0 {
		return p, nil, "provide at least one of auto_approve_movies, auto_approve_tv, can_request, quota_movies, quota_tv"
	}
	return p, detail, ""
}
