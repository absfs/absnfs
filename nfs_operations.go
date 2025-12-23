package absnfs

import (
	"io"
	"runtime"
	"strings"
)

// validateFilename validates a filename for CREATE/MKDIR operations
// Returns error status code if invalid, NFS_OK if valid
func validateFilename(name string) uint32 {
	// Check for empty name
	if name == "" {
		return NFSERR_INVAL
	}

	// Check for maximum length (255 bytes for most filesystems)
	if len(name) > 255 {
		return NFSERR_NAMETOOLONG
	}

	// Check for null bytes
	if strings.Contains(name, "\x00") {
		return NFSERR_INVAL
	}

	// Check for path separators (both forward and back slash)
	if strings.ContainsAny(name, "/\\") {
		return NFSERR_INVAL
	}

	// Check for parent directory references
	if name == "." || name == ".." {
		return NFSERR_INVAL
	}

	// Check for reserved names on Windows
	if runtime.GOOS == "windows" {
		upperName := strings.ToUpper(name)
		// Check base name without extension
		baseName := upperName
		if idx := strings.Index(upperName, "."); idx != -1 {
			baseName = upperName[:idx]
		}

		reservedNames := []string{
			"CON", "PRN", "AUX", "NUL",
			"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
			"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
		}
		for _, reserved := range reservedNames {
			if baseName == reserved {
				return NFSERR_INVAL
			}
		}
	}

	return NFS_OK
}

// validateMode validates file/directory mode for CREATE/MKDIR/SETATTR operations
// Returns error status code if invalid, NFS_OK if valid
func validateMode(mode uint32, isDir bool) uint32 {
	// Valid permission bits: 0777 (rwxrwxrwx)
	const validPermBits = 0777

	// Valid file type bits (octal 0170000)
	const fileTypeMask = 0170000

	// Extract file type bits
	fileTypeBits := mode & fileTypeMask

	// For CREATE/MKDIR, file type bits should not be set (0)
	// The file type is determined by the operation itself
	if fileTypeBits != 0 {
		return NFSERR_INVAL
	}

	// Check that only valid permission bits are set
	if mode&^validPermBits != 0 {
		return NFSERR_INVAL
	}

	// For directories, execute bits are often required for traversal
	// But we don't enforce this as it's a valid use case to create
	// directories without execute permissions (though impractical)

	return NFS_OK
}

// handleNFSCall handles NFS protocol operations using a dispatch table
func (h *NFSProcedureHandler) handleNFSCall(call *RPCCall, body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	// Check version first
	if call.Header.Version != NFS_V3 {
		reply.AcceptStatus = PROG_MISMATCH
		return reply, nil
	}

	// Look up handler in dispatch table
	handler, ok := nfsHandlers[call.Header.Procedure]
	if !ok {
		reply.AcceptStatus = PROC_UNAVAIL
		return reply, nil
	}

	return handler(h, body, reply, authCtx)
}
