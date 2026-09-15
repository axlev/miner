package collector

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-github/v62/github"
)

func TestRateLimitWaitRecognisesProviderLimitsOnly(t *testing.T) {
	reset := time.Now().Add(90 * time.Second)
	rl := &github.RateLimitError{Rate: github.Rate{Reset: github.Timestamp{Time: reset}}, Message: "API rate limit exceeded"}
	wait, ok := rateLimitWait(fmt.Errorf("failed to fetch PR #1: %w", rl))
	if !ok || wait < 90*time.Second || wait > 100*time.Second {
		t.Errorf("primary limit: wait=%v ok=%v", wait, ok)
	}
	// A reset already in the past still waits the margin, never a negative duration.
	past := &github.RateLimitError{Rate: github.Rate{Reset: github.Timestamp{Time: time.Now().Add(-time.Hour)}}}
	if wait, ok := rateLimitWait(past); !ok || wait <= 0 {
		t.Errorf("past reset: wait=%v ok=%v", wait, ok)
	}
	retry := 30 * time.Second
	abuse := &github.AbuseRateLimitError{RetryAfter: &retry}
	if wait, ok := rateLimitWait(abuse); !ok || wait < 30*time.Second {
		t.Errorf("secondary limit: wait=%v ok=%v", wait, ok)
	}
	if _, ok := rateLimitWait(errors.New("404 not found")); ok {
		t.Errorf("an ordinary error must not be treated as a rate limit")
	}
	if _, ok := rateLimitWait(nil); ok {
		t.Errorf("nil error must not wait")
	}
}
