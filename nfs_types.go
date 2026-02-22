package absnfs

// NFS status codes as defined in the NFS protocol
const (
	NFS_OK             = 0
	NFSERR_PERM        = 1
	NFSERR_NOENT       = 2
	NFSERR_IO          = 5
	NFSERR_NXIO        = 6
	NFSERR_ACCES       = 13
	NFSERR_EXIST       = 17
	NFSERR_NODEV       = 19
	NFSERR_NOTDIR      = 20
	NFSERR_ISDIR       = 21
	NFSERR_INVAL       = 22
	NFSERR_FBIG        = 27
	NFSERR_NOSPC       = 28
	NFSERR_ROFS        = 30
	NFSERR_NAMETOOLONG = 63
	NFSERR_NOTEMPTY    = 66
	NFSERR_DQUOT       = 69
	NFSERR_STALE       = 70
	NFSERR_WFLUSH      = 99
	NFSERR_BADHANDLE   = 10001 // Invalid file handle
	NFSERR_NOT_SYNC    = 10002 // Update synchronization mismatch (sattrguard3)
	NFSERR_NOTSUPP     = 10004 // Operation not supported
	NFSERR_JUKEBOX     = 10008 // Server busy, try again later (used during policy drain)
	NFSERR_DELAY       = 10013 // Server is temporarily busy (rate limit exceeded)

	// Alias for backward compatibility - use NFSERR_ACCES for NFS3 access denied errors
	ACCESS_DENIED = NFSERR_ACCES
)

// NFS3 ACCESS check constants (RFC 1813, Section 2.6)
const (
	ACCESS3_READ    = 0x0001
	ACCESS3_LOOKUP  = 0x0002
	ACCESS3_MODIFY  = 0x0004
	ACCESS3_EXTEND  = 0x0008
	ACCESS3_DELETE  = 0x0010
	ACCESS3_EXECUTE = 0x0020
)
