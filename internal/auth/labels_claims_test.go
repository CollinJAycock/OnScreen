package auth

import (
	"testing"

	"github.com/google/uuid"
)

// The label restriction rides in every token kind exactly like the rating
// ceiling, and is absent (not an empty list) for unrestricted users.
func TestTokens_CarryLabelRestriction(t *testing.T) {
	tm, err := NewTokenMaker(DeriveKey32("test-secret-key-that-is-32-bytes!"))
	if err != nil {
		t.Fatal(err)
	}
	claims := Claims{
		UserID:        uuid.New(),
		Username:      "kid",
		BlockedLabels: []string{"horror", "war"},
		AllowedLabels: []string{"kids"},
	}
	issue := map[string]func(Claims) (string, error){
		"access": tm.IssueAccessToken,
		"asset":  tm.IssueAssetToken,
		"stream": func(c Claims) (string, error) { return tm.IssueStreamToken(c, uuid.New()) },
	}
	for kind, fn := range issue {
		tok, err := fn(claims)
		if err != nil {
			t.Fatalf("%s: issue: %v", kind, err)
		}
		got, err := tm.ValidateAccessToken(tok)
		if err != nil {
			t.Fatalf("%s: validate: %v", kind, err)
		}
		f := got.LabelFilter()
		if len(f.Blocked) != 2 || f.Blocked[0] != "horror" || f.Blocked[1] != "war" {
			t.Errorf("%s: blocked = %v", kind, f.Blocked)
		}
		if len(f.Allowed) != 1 || f.Allowed[0] != "kids" {
			t.Errorf("%s: allowed = %v", kind, f.Allowed)
		}
	}

	tok, err := tm.IssueAccessToken(Claims{UserID: uuid.New(), Username: "adult"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := tm.ValidateAccessToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	if got.LabelFilter().Active() || got.BlockedLabels != nil || got.AllowedLabels != nil {
		t.Errorf("unrestricted token decoded a restriction: %+v / %+v", got.BlockedLabels, got.AllowedLabels)
	}
}

func TestClaims_LabelFilterNilSafe(t *testing.T) {
	var c *Claims
	if c.LabelFilter().Active() {
		t.Error("nil claims must carry no restriction")
	}
}
