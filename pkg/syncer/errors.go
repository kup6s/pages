// Package syncer - classified sync errors
package syncer

import (
	"context"
	"errors"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

// syncError carries the Synced condition reason of a failed sync.
type syncError struct {
	Reason string
	Err    error
}

func (e *syncError) Error() string { return e.Err.Error() }
func (e *syncError) Unwrap() error { return e.Err }

func newSyncError(reason string, err error) error {
	return &syncError{Reason: reason, Err: err}
}

// syncReason returns the condition reason of err.
func syncReason(err error) string {
	var se *syncError
	if errors.As(err, &se) {
		return se.Reason
	}
	return pagesv1.SyncReasonSyncFailed
}

// classifyGitError picks Timeout or AuthFailed when they apply, else fallback.
func classifyGitError(fallback string, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return newSyncError(pagesv1.SyncReasonTimeout, err)
	case isAuthError(err):
		return newSyncError(pagesv1.SyncReasonAuthFailed, err)
	default:
		return newSyncError(fallback, err)
	}
}
