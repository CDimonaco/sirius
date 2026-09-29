package sipsrv

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestInviteErrorNamesTheReason(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		lastStatus int
		want       string
	}{
		{name: "declined", err: errors.New("dialog failed"), lastStatus: 486, want: "declined with 486"},
		{name: "no answer", err: context.DeadlineExceeded, want: "did not answer within 30s"},
		{name: "codec mismatch", err: errors.New("no common codec"), want: "invite: no common codec"},
		// A refusal wins over the deadline: the phone said no before the clock ran out.
		{name: "declined at the deadline", err: context.DeadlineExceeded, lastStatus: 603, want: "declined with 603"},
	} {
		got := inviteError(tc.err, tc.lastStatus)
		if !strings.Contains(got.Error(), tc.want) {
			t.Errorf("%s: got %q, want it to mention %q", tc.name, got, tc.want)
		}
		if !errors.Is(got, tc.err) {
			t.Errorf("%s: wrapped error was lost", tc.name)
		}
	}
}
