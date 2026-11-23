package pkg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"time"
)

const (
	registryLoginTimeout = 45 * time.Second
	imageTransferTimeout = 30 * time.Minute
	retryAttempts        = 3
	retryInitialDelay    = 2 * time.Second
)

// retryWithTimeout bounds each network operation and retries only errors that
// are likely to be transient. The parent context remains the overall deadline.
func retryWithTimeout(
	ctx context.Context,
	attemptTimeout time.Duration,
	maxAttempts int,
	initialDelay time.Duration,
	operation func(context.Context) error,
) error {
	if maxAttempts < 1 {
		return errors.New("maxAttempts must be at least 1")
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		lastErr = operation(attemptCtx)
		cancel()

		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt == maxAttempts || !isTransientError(lastErr) {
			return fmt.Errorf("operation failed after %d attempt(s): %w", attempt, lastErr)
		}

		delay := initialDelay * time.Duration(1<<(attempt-1))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}

	return lastErr
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ETIMEDOUT) ||
		errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.EHOSTUNREACH) {
		return true
	}
	if errors.Is(err, context.Canceled) {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}

	message := strings.ToLower(err.Error())
	transientMessages := []string{
		"connection reset by peer",
		"connection refused",
		"broken pipe",
		"i/o timeout",
		"tls handshake timeout",
		"unexpected eof",
		"temporary failure in name resolution",
		"server misbehaving",
		"too many requests",
		"429 too many requests",
		"408 request timeout",
		"500 internal server error",
		"502 bad gateway",
		"503 service unavailable",
		"504 gateway timeout",
	}
	for _, marker := range transientMessages {
		if strings.Contains(message, marker) {
			return true
		}
	}

	return false
}
