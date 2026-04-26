package v1

import (
	"strings"
	"testing"
)

func TestValidatePassword_RejectsTooShort(t *testing.T) {
	cases := []string{
		"",
		"a",
		"shortpw",
		"elevenchar1",  // 11 chars — one below floor
	}
	for _, pw := range cases {
		err := ValidatePassword(pw)
		if err == nil {
			t.Errorf("expected error for %q (len=%d), got nil", pw, len(pw))
		}
	}
}

func TestValidatePassword_AcceptsAtAndAboveFloor(t *testing.T) {
	cases := []string{
		"twelvecharsX",                                                   // exactly the floor
		"thirteenchars1",                                                 // one above
		"a-very-long-and-secure-passphrase-with-many-characters-indeed", // long
		"\x00\x01\x02unicodeさようなら",                                       // arbitrary bytes
	}
	for _, pw := range cases {
		if err := ValidatePassword(pw); err != nil {
			t.Errorf("unexpected error for %q (len=%d): %v", pw, len(pw), err)
		}
	}
}

func TestValidatePassword_ErrorMessageMentionsLength(t *testing.T) {
	// The error string is surfaced verbatim to API clients; it must say
	// what the user needs to do, not just "invalid".
	err := ValidatePassword("short")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "12") {
		t.Errorf("error %q should mention the 12-char floor", err.Error())
	}
}

func TestMinPasswordLengthIs12(t *testing.T) {
	// Constant guard — every credential-creation path lifts MinPasswordLength
	// from this constant. A drift here breaks the cross-handler invariant
	// that admin-reset, self-reset, register, and invite all enforce the
	// same floor.
	if MinPasswordLength != 12 {
		t.Errorf("MinPasswordLength = %d, want 12 (would split admin vs self paths again)", MinPasswordLength)
	}
}
