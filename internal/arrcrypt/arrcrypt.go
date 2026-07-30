// Package arrcrypt seals and opens the per-service API keys stored in
// arr_services.api_key.
//
// The single-service `arr_api_key` setting has been encrypted at rest since the
// settings service grew an Encryptor, but the multi-service table that replaced
// it stores its keys in cleartext — so a database dump, a backup, or any
// read-only SQL injection hands over working Sonarr/Radarr credentials, which
// carry full control of the downloader and its filesystem paths. Those keys are
// never echoed back through the API (the DTO exposes only api_key_set), so this
// is purely an at-rest gap, and closing it costs one prefix check on read.
//
// The envelope matches internal/domain/settings: a sentinel prefix plus an
// AES-GCM ciphertext bound to associated data. The row id is the associated
// data, so a ciphertext lifted from one service row fails to authenticate in
// another instead of silently making that service use the first one's key.
//
// Reads are prefix-dispatched, so rows written before this existed keep working
// untouched and upgrade the next time they are saved. That is what makes this
// deployable without a data migration — and what makes a missed write path fail
// safe (a key stays plaintext) rather than breaking the integration.
package arrcrypt

import (
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
)

// prefix marks a sealed api_key. Unprefixed values are legacy cleartext.
const prefix = "arrenc1:"

// Seal encrypts key for storage against the given row id. A nil encryptor
// (at-rest encryption not configured) returns the key unchanged, matching how
// the settings service degrades. An empty key stays empty — callers use ""
// to mean "no credential", and sealing it would make that unreadable.
func Seal(enc *auth.Encryptor, id uuid.UUID, key string) (string, error) {
	if enc == nil || key == "" {
		return key, nil
	}
	ct, err := enc.EncryptContext(key, id.String())
	if err != nil {
		return "", err
	}
	return prefix + ct, nil
}

// Open decrypts a stored api_key. Values without the sentinel are returned
// as-is: they were written before at-rest encryption and are still valid
// credentials. A sealed value with no encryptor available is an error rather
// than a pass-through, since handing ciphertext to an arr client would surface
// as a confusing 401 from the far side.
func Open(enc *auth.Encryptor, id uuid.UUID, stored string) (string, error) {
	if !strings.HasPrefix(stored, prefix) {
		return stored, nil
	}
	if enc == nil {
		return "", errNoEncryptor
	}
	return enc.DecryptContext(strings.TrimPrefix(stored, prefix), id.String())
}

// IsSealed reports whether a stored value carries the sentinel. Used by
// rotate-key to tell a row that needs upgrading from one already current.
func IsSealed(stored string) bool { return strings.HasPrefix(stored, prefix) }

type constErr string

func (e constErr) Error() string { return string(e) }

const errNoEncryptor = constErr("arr api key is encrypted but no encryptor is configured")
