package v1

import "github.com/onscreen/onscreen/internal/i18n"

// MinPasswordLength is the floor every credential-creation and credential-
// reset path enforces. Lifted to a constant so the registration, invite,
// self-service reset, and admin-initiated reset paths cannot drift apart
// (the admin path was historically 8 while the others were 12 — that
// asymmetry made the admin onboarding flow the weakest link).
const MinPasswordLength = 12

// ValidatePassword returns an error if pw fails any baseline credential
// rule. The error is a [userError]: write it with [badRequestErr], which
// sends the rejection reason in the request's language.
func ValidatePassword(pw string) error {
	if len(pw) < MinPasswordLength {
		return newUserError("error.password.tooShort", i18n.Params{"min": MinPasswordLength})
	}
	return nil
}
