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
	NFSERR_NOTSUPP     = 10004 // Operation not supported
	NFSERR_DELAY       = 10013 // Server is temporarily busy (rate limit exceeded)

	// Alias for backward compatibility - use NFSERR_ACCES for NFS3 access denied errors
	ACCESS_DENIED = NFSERR_ACCES
)

// FileHandle represents an NFS file handle
type FileHandle struct {
	Handle uint64
}

// FileAttribute represents NFS file attributes
type FileAttribute struct {
	Type                uint32
	Mode                uint32
	Nlink               uint32
	Uid                 uint32
	Gid                 uint32
	Size                uint64
	Used                uint64
	SpecData            [2]uint32
	Fsid                uint64
	Fileid              uint64
	Atime, Mtime, Ctime uint32
}

// DirOpArg represents arguments for directory operations
type DirOpArg struct {
	Handle FileHandle
	Name   string
}

// CreateArgs represents arguments for file creation
type CreateArgs struct {
	Where DirOpArg
	Mode  uint32
}

// RenameArgs represents arguments for rename operations
type RenameArgs struct {
	From DirOpArg
	To   DirOpArg
}

// ReadArgs represents arguments for read operations
type ReadArgs struct {
	Handle FileHandle
	Offset uint64
	Count  uint32
}

// WriteArgs represents arguments for write operations
type WriteArgs struct {
	Handle FileHandle
	Offset uint64
	Data   []byte
}

// Entry represents a directory entry
type Entry struct {
	FileId  uint64
	Name    string
	Cookie  uint64
	NextObj *Entry
}

// ReadDirArgs represents arguments for readdir operations
type ReadDirArgs struct {
	Handle FileHandle
	Cookie uint64
	Count  uint32
}

// ReadDirRes represents the result of a readdir operation
type ReadDirRes struct {
	Status uint32
	Reply  struct {
		Entries *Entry
		EOF     bool
	}
}

// FSInfo represents filesystem information
type FSInfo struct {
	MaxFileSize    uint64
	SpaceAvail     uint64
	SpaceTotal     uint64
	SpaceFree      uint64
	FileSlotsFree  uint32
	FileSlotsTotal uint32
	Properties     uint32
}

// FSStats represents filesystem statistics
type FSStats struct {
	TotalBytes uint64
	FreeBytes  uint64
	AvailBytes uint64
	TotalFiles uint64
	FreeFiles  uint64
	AvailFiles uint64
	InvarSec   uint32
}
