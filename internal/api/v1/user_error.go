package v1

import (
	"errors"
	"net/http"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/i18n"
)

// userError is a validation failure a person sees in an app: a catalog key
// (internal/i18n, locales/*/errors.json) with its placeholders. Error() is
// the English text, so logs and tests that read err.Error() see what they
// always did; the handler writes it with [badRequestErr] or
// [validationErr], which send it in the request's language.
type userError struct {
	key    string
	params i18n.Params
}

func (e *userError) Error() string { return i18n.T(i18n.English, e.key, e.params) }

// newUserError is a [userError] for key with params.
func newUserError(key string, params i18n.Params) error {
	return &userError{key: key, params: params}
}

// badRequestErr writes err as a 400: translated when it is (or wraps) a
// [userError], else its text as is (a developer-facing message).
func badRequestErr(w http.ResponseWriter, r *http.Request, err error) {
	var ue *userError
	if errors.As(err, &ue) {
		respond.BadRequestT(w, r, ue.key, ue.params)
		return
	}
	respond.BadRequest(w, r, err.Error())
}

// validationErr writes err as a 422, translated like [badRequestErr].
func validationErr(w http.ResponseWriter, r *http.Request, err error) {
	var ue *userError
	if errors.As(err, &ue) {
		respond.ValidationErrorT(w, r, ue.key, ue.params)
		return
	}
	respond.ValidationError(w, r, err.Error())
}
