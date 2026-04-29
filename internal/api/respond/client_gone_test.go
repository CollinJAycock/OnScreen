package respond

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"
)

func TestIsClientGone(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("disk full"), false},
		{"context.Canceled", context.Canceled, true},
		{"context.DeadlineExceeded", context.DeadlineExceeded, true},
		{"wrapped Canceled", fmt.Errorf("query: %w", context.Canceled), true},
		{"EPIPE", syscall.EPIPE, true},
		{"ECONNRESET", syscall.ECONNRESET, true},
		{"net.ErrClosed", net.ErrClosed, true},
		{"io.ErrClosedPipe", io.ErrClosedPipe, true},
		// fmt.Errorf without %w loses the chain — has to be detected by
		// substring as a fallback.
		{"unwrapped broken pipe string", errors.New("write tcp 10.0.0.1:80: write: broken pipe"), true},
		{"unwrapped reset string", errors.New("read: connection reset by peer"), true},
		// Real DB error must NOT match.
		{"sql syntax error", errors.New("pq: syntax error at or near \"FORM\""), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsClientGone(tc.err); got != tc.want {
				t.Errorf("IsClientGone(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
