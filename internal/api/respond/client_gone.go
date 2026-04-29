package respond

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
)

// IsClientGone returns true when err looks like the request died because
// the client closed the connection or the request context was cancelled,
// rather than the server hitting a real failure. Use it to skip noisy
// ERROR/WARN logging at the handler level for normal user behaviour
// (navigating away mid-image-fetch, typing fast in search, etc.).
//
// Recognises:
//   - context.Canceled        — request context torn down (client closed)
//   - context.DeadlineExceeded — deadline elapsed; same intent as above for
//     our purposes (request can no longer be served)
//   - syscall.EPIPE / ECONNRESET — TCP write failed because the peer left
//   - "broken pipe" / "connection reset by peer" — same conditions reported
//     via wrapped errors that don't unwrap to the syscall constants
//   - net.ErrClosed — listener / write to a closed conn
//   - io.ErrClosedPipe — internal pipe closed
//
// Anything else is a real server-side failure and should still be logged.
func IsClientGone(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	// Fallback for wrappers that lose the syscall errno (e.g. some
	// ffmpeg pipe paths surface "write |1: broken pipe" as a plain
	// fmt.Errorf without %w).
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer")
}
