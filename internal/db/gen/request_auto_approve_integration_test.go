//go:build integration

// Round-trips against a real Postgres testcontainer for migration 00020
// (media-request auto-approval): the users.auto_approve_movies / _tv and
// media_requests.auto_approved columns, the full-account INSERTs that carry
// the new-account defaults, the managed-profile INSERT that must not, and the
// queries the request service and admin endpoint use.
//
// Run with: go test -tags=integration ./internal/db/gen/ -run AutoApprove
package gen_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestAutoApprove_Integration_UserColumnsAndQueries(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	suffix := uuid.New().String()[:8]
	str := func(s string) *string { return &s }

	// A pre-existing style insert that doesn't name the columns lands on the
	// column defaults: both off.
	plain := seedUser(ctx, t, q, "plain-"+suffix)
	perm, err := q.GetUserRequestPermissions(ctx, plain)
	if err != nil {
		t.Fatalf("GetUserRequestPermissions: %v", err)
	}
	if perm.AutoApproveMovies || perm.AutoApproveTv || perm.IsAdmin || perm.MaxContentRating != nil {
		t.Errorf("defaulted user = %+v, want all off / no ceiling", perm)
	}

	// Every full-account INSERT stores the toggles it is given.
	local, err := q.CreateUser(ctx, gen.CreateUserParams{
		Username: "local-" + suffix, PasswordHash: str("h"), AutoApproveMovies: true,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	oidc, err := q.CreateOIDCUser(ctx, gen.CreateOIDCUserParams{
		Username: "oidc-" + suffix, OidcIssuer: str("https://idp"), OidcSubject: str("s-" + suffix),
		AutoApproveTv: true,
	})
	if err != nil {
		t.Fatalf("CreateOIDCUser: %v", err)
	}
	saml, err := q.CreateSAMLUser(ctx, gen.CreateSAMLUserParams{
		Username: "saml-" + suffix, SamlIssuer: str("idp"), SamlSubject: str("s-" + suffix),
		AutoApproveMovies: true, AutoApproveTv: true,
	})
	if err != nil {
		t.Fatalf("CreateSAMLUser: %v", err)
	}
	ldapUser, err := q.CreateLDAPUser(ctx, gen.CreateLDAPUserParams{
		Username: "ldap-" + suffix, LdapDn: str("uid=" + suffix), AutoApproveMovies: true,
	})
	if err != nil {
		t.Fatalf("CreateLDAPUser: %v", err)
	}
	for _, tc := range []struct {
		name       string
		u          gen.User
		movies, tv bool
	}{
		{"local", local, true, false},
		{"oidc", oidc, false, true},
		{"saml", saml, true, true},
		{"ldap", ldapUser, true, false},
	} {
		// Both the RETURNING * row and a fresh read agree.
		if tc.u.AutoApproveMovies != tc.movies || tc.u.AutoApproveTv != tc.tv {
			t.Errorf("%s returned movies=%v tv=%v, want %v/%v", tc.name, tc.u.AutoApproveMovies, tc.u.AutoApproveTv, tc.movies, tc.tv)
		}
		p, err := q.GetUserRequestPermissions(ctx, tc.u.ID)
		if err != nil {
			t.Fatalf("%s GetUserRequestPermissions: %v", tc.name, err)
		}
		if p.AutoApproveMovies != tc.movies || p.AutoApproveTv != tc.tv {
			t.Errorf("%s stored movies=%v tv=%v, want %v/%v", tc.name, p.AutoApproveMovies, p.AutoApproveTv, tc.movies, tc.tv)
		}
	}

	// A managed profile starts with both off even when its owner has both on.
	prof, err := q.CreateManagedProfile(ctx, gen.CreateManagedProfileParams{
		Username:     "kid-" + suffix,
		ParentUserID: pgtype.UUID{Bytes: saml.ID, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateManagedProfile: %v", err)
	}
	pp, err := q.GetUserRequestPermissions(ctx, prof.ID)
	if err != nil {
		t.Fatalf("GetUserRequestPermissions(profile): %v", err)
	}
	if pp.AutoApproveMovies || pp.AutoApproveTv {
		t.Errorf("managed profile = %+v, want both off regardless of the owner", pp)
	}

	// The admin endpoint's write: stored as given — even alongside a
	// content-rating ceiling, which the service (not the DB) makes win.
	if err := q.UpdateUserContentRating(ctx, gen.UpdateUserContentRatingParams{
		ID: plain, MaxContentRating: str("PG-13"),
	}); err != nil {
		t.Fatalf("UpdateUserContentRating: %v", err)
	}
	on := true
	n, err := q.SetUserRequestPermissions(ctx, gen.SetUserRequestPermissionsParams{
		ID: plain, AutoApproveMovies: &on, AutoApproveTv: &on,
	})
	if err != nil || n != 1 {
		t.Fatalf("SetUserRequestPermissions = (%d, %v), want (1, nil)", n, err)
	}
	perm, err = q.GetUserRequestPermissions(ctx, plain)
	if err != nil {
		t.Fatalf("GetUserRequestPermissions: %v", err)
	}
	if !perm.AutoApproveMovies || !perm.AutoApproveTv || perm.MaxContentRating == nil || *perm.MaxContentRating != "PG-13" {
		t.Errorf("after set = %+v, want both on with the PG-13 ceiling kept", perm)
	}
	if n, err := q.SetUserRequestPermissions(ctx, gen.SetUserRequestPermissionsParams{ID: uuid.New()}); err != nil || n != 0 {
		t.Errorf("unknown user: (%d, %v), want (0, nil) so the handler can 404", n, err)
	}

	// The admin user list exposes the stored toggles.
	rows, err := q.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.ID == oidc.ID {
			found = true
			if r.AutoApproveMovies || !r.AutoApproveTv {
				t.Errorf("ListUsers oidc row = %+v, want movies=false tv=true", r)
			}
		}
	}
	if !found {
		t.Error("ListUsers missing the oidc user")
	}
}

func TestAutoApprove_Integration_ApproveMediaRequestFlag(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	suffix := uuid.New().String()[:8]

	requester := seedUser(ctx, t, q, "req-"+suffix)
	admin := seedUser(ctx, t, q, "adm-"+suffix)
	svc, err := q.CreateArrService(ctx, gen.CreateArrServiceParams{
		ID: uuid.New(), Name: "radarr-" + suffix, Kind: "radarr",
		BaseUrl: "http://radarr:7878", ApiKey: "k", DefaultTags: []byte("[]"),
		IsDefault: true, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateArrService: %v", err)
	}
	newReq := func(tmdb int32) gen.MediaRequest {
		t.Helper()
		r, err := q.CreateMediaRequest(ctx, gen.CreateMediaRequestParams{
			UserID: requester, Type: "movie", TmdbID: tmdb, Title: "t", Status: "pending",
		})
		if err != nil {
			t.Fatalf("CreateMediaRequest: %v", err)
		}
		if r.AutoApproved {
			t.Fatal("new request row defaulted auto_approved=true")
		}
		return r
	}
	svcID := pgtype.UUID{Bytes: svc.ID, Valid: true}

	// Automatic: NULL decided_by, flag set, decided_at stamped.
	auto := newReq(949)
	got, err := q.ApproveMediaRequest(ctx, gen.ApproveMediaRequestParams{
		ID: auto.ID, ServiceID: svcID, AutoApproved: true,
	})
	if err != nil {
		t.Fatalf("ApproveMediaRequest(auto): %v", err)
	}
	if got.Status != "approved" || !got.AutoApproved || got.DecidedBy.Valid || !got.DecidedAt.Valid {
		t.Errorf("auto-approved row = status %q auto %v decided_by %v decided_at %v; want approved/true/NULL/set",
			got.Status, got.AutoApproved, got.DecidedBy, got.DecidedAt.Valid)
	}
	// It survives the downloading transition and a re-read.
	if err := q.MarkMediaRequestDownloading(ctx, auto.ID); err != nil {
		t.Fatalf("MarkMediaRequestDownloading: %v", err)
	}
	reread, err := q.GetMediaRequest(ctx, auto.ID)
	if err != nil {
		t.Fatalf("GetMediaRequest: %v", err)
	}
	if reread.Status != "downloading" || !reread.AutoApproved {
		t.Errorf("re-read = status %q auto %v, want downloading/true", reread.Status, reread.AutoApproved)
	}

	// Manual: the admin is recorded and the flag stays false.
	manual := newReq(950)
	got, err = q.ApproveMediaRequest(ctx, gen.ApproveMediaRequestParams{
		ID: manual.ID, ServiceID: svcID, DecidedBy: pgtype.UUID{Bytes: admin, Valid: true},
	})
	if err != nil {
		t.Fatalf("ApproveMediaRequest(manual): %v", err)
	}
	if got.AutoApproved || !got.DecidedBy.Valid || got.DecidedBy.Bytes != admin {
		t.Errorf("manual row = auto %v decided_by %v, want false/%s", got.AutoApproved, got.DecidedBy, admin)
	}
}
