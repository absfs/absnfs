package absnfs

import "fmt"

// InvalidFileHandleError represents an error when a file handle is invalid
type InvalidFileHandleError struct {
	Handle uint64
	Reason string
}

// Error implements the error interface for InvalidFileHandleError
func (e *InvalidFileHandleError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("invalid file handle %d: %s", e.Handle, e.Reason)
	}
	return fmt.Sprintf("invalid file handle %d", e.Handle)
}

// NotSupportedError represents an error when an operation is not supported
type NotSupportedError struct {
	Operation string
	Reason    string
}

// Error implements the error interface for NotSupportedError
func (e *NotSupportedError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("operation '%s' not supported: %s", e.Operation, e.Reason)
	}
	return fmt.Sprintf("operation '%s' not supported", e.Operation)
}
