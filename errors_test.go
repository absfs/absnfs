package absnfs

import (
	"errors"
	"testing"
)

// TestInvalidFileHandleError tests the InvalidFileHandleError type
func TestInvalidFileHandleError(t *testing.T) {
	tests := []struct {
		name        string
		handle      uint64
		reason      string
		expectedMsg string
	}{
		{
			name:        "with reason",
			handle:      12345,
			reason:      "handle not found in file handle map",
			expectedMsg: "invalid file handle 12345: handle not found in file handle map",
		},
		{
			name:        "without reason",
			handle:      67890,
			reason:      "",
			expectedMsg: "invalid file handle 67890",
		},
		{
			name:        "zero handle",
			handle:      0,
			reason:      "zero handle is invalid",
			expectedMsg: "invalid file handle 0: zero handle is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &InvalidFileHandleError{
				Handle: tt.handle,
				Reason: tt.reason,
			}

			if err.Error() != tt.expectedMsg {
				t.Errorf("Error() = %q, want %q", err.Error(), tt.expectedMsg)
			}

			// Test that it implements error interface
			var _ error = err
		})
	}
}

// TestNotSupportedError tests the NotSupportedError type
func TestNotSupportedError(t *testing.T) {
	tests := []struct {
		name        string
		operation   string
		reason      string
		expectedMsg string
	}{
		{
			name:        "with reason",
			operation:   "LINK",
			reason:      "hard links are not supported",
			expectedMsg: "operation 'LINK' not supported: hard links are not supported",
		},
		{
			name:        "without reason",
			operation:   "MKNOD",
			reason:      "",
			expectedMsg: "operation 'MKNOD' not supported",
		},
		{
			name:        "empty operation",
			operation:   "",
			reason:      "operation not implemented",
			expectedMsg: "operation '' not supported: operation not implemented",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &NotSupportedError{
				Operation: tt.operation,
				Reason:    tt.reason,
			}

			if err.Error() != tt.expectedMsg {
				t.Errorf("Error() = %q, want %q", err.Error(), tt.expectedMsg)
			}

			// Test that it implements error interface
			var _ error = err
		})
	}
}

// TestMapErrorWithCustomErrors tests that mapError correctly maps custom errors
func TestMapErrorWithCustomErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected uint32
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: NFS_OK,
		},
		{
			name: "InvalidFileHandleError",
			err: &InvalidFileHandleError{
				Handle: 123,
				Reason: "test",
			},
			expected: NFSERR_BADHANDLE,
		},
		{
			name: "NotSupportedError",
			err: &NotSupportedError{
				Operation: "LINK",
				Reason:    "test",
			},
			expected: NFSERR_NOTSUPP,
		},
		{
			name: "wrapped InvalidFileHandleError",
			err: wrapError(&InvalidFileHandleError{
				Handle: 456,
				Reason: "wrapped",
			}),
			expected: NFSERR_BADHANDLE,
		},
		{
			name: "wrapped NotSupportedError",
			err: wrapError(&NotSupportedError{
				Operation: "MKNOD",
				Reason:    "wrapped",
			}),
			expected: NFSERR_NOTSUPP,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapError(tt.err)
			if result != tt.expected {
				t.Errorf("mapError() = %d, want %d", result, tt.expected)
			}
		})
	}
}

// TestErrorsAsDetection tests that errors.As() correctly detects custom errors
func TestErrorsAsDetection(t *testing.T) {
	t.Run("detect InvalidFileHandleError", func(t *testing.T) {
		err := &InvalidFileHandleError{
			Handle: 999,
			Reason: "test",
		}

		var target *InvalidFileHandleError
		if !errors.As(err, &target) {
			t.Error("errors.As() failed to detect InvalidFileHandleError")
		}

		if target.Handle != 999 {
			t.Errorf("Handle = %d, want 999", target.Handle)
		}
		if target.Reason != "test" {
			t.Errorf("Reason = %q, want %q", target.Reason, "test")
		}
	})

	t.Run("detect wrapped InvalidFileHandleError", func(t *testing.T) {
		innerErr := &InvalidFileHandleError{
			Handle: 888,
			Reason: "wrapped error",
		}
		wrappedErr := wrapError(innerErr)

		var target *InvalidFileHandleError
		if !errors.As(wrappedErr, &target) {
			t.Error("errors.As() failed to detect wrapped InvalidFileHandleError")
		}

		if target.Handle != 888 {
			t.Errorf("Handle = %d, want 888", target.Handle)
		}
	})

	t.Run("detect NotSupportedError", func(t *testing.T) {
		err := &NotSupportedError{
			Operation: "TEST_OP",
			Reason:    "testing",
		}

		var target *NotSupportedError
		if !errors.As(err, &target) {
			t.Error("errors.As() failed to detect NotSupportedError")
		}

		if target.Operation != "TEST_OP" {
			t.Errorf("Operation = %q, want %q", target.Operation, "TEST_OP")
		}
		if target.Reason != "testing" {
			t.Errorf("Reason = %q, want %q", target.Reason, "testing")
		}
	})

	t.Run("detect wrapped NotSupportedError", func(t *testing.T) {
		innerErr := &NotSupportedError{
			Operation: "WRAPPED_OP",
			Reason:    "wrapped",
		}
		wrappedErr := wrapError(innerErr)

		var target *NotSupportedError
		if !errors.As(wrappedErr, &target) {
			t.Error("errors.As() failed to detect wrapped NotSupportedError")
		}

		if target.Operation != "WRAPPED_OP" {
			t.Errorf("Operation = %q, want %q", target.Operation, "WRAPPED_OP")
		}
	})

	t.Run("should not detect wrong error type", func(t *testing.T) {
		err := &InvalidFileHandleError{
			Handle: 777,
			Reason: "test",
		}

		var target *NotSupportedError
		if errors.As(err, &target) {
			t.Error("errors.As() incorrectly detected NotSupportedError from InvalidFileHandleError")
		}
	})
}

// TestFileHandleMapGetOrError tests the GetOrError method
func TestFileHandleMapGetOrError(t *testing.T) {
	// Skip this test - it requires a real FileHandleMap with proper initialization
	// The functionality is tested through integration tests
	t.Skip("Skipping FileHandleMapGetOrError test - requires full FileHandleMap setup")
}

// TestErrorConstantsExist tests that the new error constants are defined
func TestErrorConstantsExist(t *testing.T) {
	// Test that NFSERR_BADHANDLE is defined and has correct value
	if NFSERR_BADHANDLE != 10001 {
		t.Errorf("NFSERR_BADHANDLE = %d, want 10001", NFSERR_BADHANDLE)
	}

	// Test that NFSERR_NOTSUPP is defined and has correct value
	if NFSERR_NOTSUPP != 10004 {
		t.Errorf("NFSERR_NOTSUPP = %d, want 10004", NFSERR_NOTSUPP)
	}

	// Verify NFSERR_STALE still exists
	if NFSERR_STALE != 70 {
		t.Errorf("NFSERR_STALE = %d, want 70", NFSERR_STALE)
	}
}

// Helper function to wrap errors for testing
func wrapError(err error) error {
	return &wrappedError{inner: err}
}

type wrappedError struct {
	inner error
}

func (e *wrappedError) Error() string {
	return "wrapped: " + e.inner.Error()
}

func (e *wrappedError) Unwrap() error {
	return e.inner
}
