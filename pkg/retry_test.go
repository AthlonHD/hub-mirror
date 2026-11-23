package pkg

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIsTransientError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "connection reset", err: errors.New("read tcp: connection reset by peer"), want: true},
		{name: "temporary gateway error", err: errors.New("503 Service Unavailable"), want: true},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: true},
		{name: "authentication rejected", err: errors.New("unauthorized: 401"), want: false},
		{name: "forbidden", err: errors.New("403 Forbidden"), want: false},
		{name: "invalid image", err: errors.New("manifest unknown"), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isTransientError(test.err); got != test.want {
				t.Fatalf("isTransientError(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestRetryWithTimeoutRetriesTransientError(t *testing.T) {
	attempts := 0
	err := retryWithTimeout(context.Background(), time.Second, 3, time.Millisecond, func(context.Context) error {
		attempts++
		if attempts == 1 {
			return errors.New("read tcp: connection reset by peer")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retryWithTimeout() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("operation called %d times, want 2", attempts)
	}
}

func TestRetryWithTimeoutDoesNotRetryPermanentError(t *testing.T) {
	attempts := 0
	err := retryWithTimeout(context.Background(), time.Second, 3, time.Millisecond, func(context.Context) error {
		attempts++
		return errors.New("unauthorized: 401")
	})
	if err == nil {
		t.Fatal("retryWithTimeout() error = nil, want authentication error")
	}
	if attempts != 1 {
		t.Fatalf("operation called %d times, want 1", attempts)
	}
}

func TestRetryWithTimeoutStopsWhenParentContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts := 0
	err := retryWithTimeout(ctx, time.Second, 3, time.Millisecond, func(context.Context) error {
		attempts++
		return errors.New("connection reset by peer")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("retryWithTimeout() error = %v, want context.Canceled", err)
	}
	if attempts != 0 {
		t.Fatalf("operation called %d times, want 0", attempts)
	}
}
