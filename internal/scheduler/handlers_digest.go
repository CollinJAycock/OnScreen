package scheduler

import (
	"context"
	"encoding/json"
)

// DigestSender sends the digests that are due. Satisfied by *digest.Service.
type DigestSender interface {
	SendDue(ctx context.Context) (string, error)
}

// NewDigestSendHandler returns the digest_send task: email every opted-in
// user the digest of their current week or month when it's due and they
// haven't had it yet. Each user gets a period at most once (the period is
// claimed before sending), empty digests are skipped, and sends are paced.
// It does nothing while the digest is off in Settings ▸ Email or SMTP isn't
// configured.
func NewDigestSendHandler(s DigestSender) HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (string, error) {
		return s.SendDue(ctx)
	}
}
