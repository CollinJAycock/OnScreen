package settings

import "testing"

// The scrobble apps' configs carry secrets (the Last.fm shared secret, the
// Trakt client secret), so they must be on the encrypted-at-rest allowlist;
// rotate-key then re-seals them with the rest of server_settings.
func TestScrobbleAppConfigsAreEncryptedAtRest(t *testing.T) {
	s := &Service{}
	for _, key := range []string{keyLastFMConfig, keyTraktConfig} {
		if !s.isSecretKey(key) {
			t.Errorf("%s must be in secretKeys", key)
		}
	}
}
