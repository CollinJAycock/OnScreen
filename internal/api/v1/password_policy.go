package v1

import "fmt"

// MinPasswordLength is the floor every credential-creation and credential-
// reset path enforces. Lifted to a constant so the registration, invite,
// self-service reset, and admin-initiated reset paths cannot drift apart
// (the admin path was historically 8 while the others were 12 — that
// asymmetry made the admin onboarding flow the weakest link).
const MinPasswordLength = 12

// ValidatePassword returns an error if pw fails any baseline credential
// rule. The string in the error is safe to surface to the client as the
// rejection reason.
func ValidatePassword(pw string) error {
	if len(pw) < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	return nil
}
