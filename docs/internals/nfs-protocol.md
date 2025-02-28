---
layout: default
title: NFS Protocol Overview
---

# NFS Protocol Overview

This document provides an overview of the Network File System (NFS) protocol as implemented in ABSNFS. Understanding the protocol is valuable for developers working with or extending ABSNFS.

## Introduction to NFS

Network File System (NFS) is a distributed file system protocol that allows a client computer to access files over a network as if they were stored locally. Developed by Sun Microsystems, NFS has become a standard for network file sharing.

NFS is designed to be:
- **Stateless**: The server doesn't need to maintain client state (though NFSv4 introduced more statefulness)
- **Platform-independent**: Works across different operating systems
- **Transparent**: Applications can work with remote files as if they were local
- **Network resilient**: Can recover from network interruptions
- **Scalable**: Supports many concurrent clients

## NFS Protocol Versions

NFS has evolved through several versions:

| Version | Status | Key Features |
|---------|--------|--------------|
| NFSv2   | Legacy | Original protocol, limited file size |
| NFSv3   | Widely used | Better error handling, larger files, asynchronous writes |
| NFSv4   | Current | Stateful operation, integrated security, firewall friendliness |
| NFSv4.1 | Current | Parallel access to servers (pNFS), sessions |
| NFSv4.2 | Current | Server-side copy, space reservation, sparse files |

ABSNFS currently implements NFSv3, which balances simplicity and functionality.

## NFS Architecture

NFS follows a client-server architecture:

1. **NFS Client**: Mounts remote filesystems and translates file operations into NFS protocol requests
2. **NFS Server**: Handles requests and performs operations on the underlying filesystem
3. **RPC Layer**: Remote Procedure Call mechanism that allows clients to invoke procedures on the server
4. **XDR**: External Data Representation that ensures data compatibility across different architectures

## NFS Protocol Structure

### Protocol Layers

NFS operates as a stack of protocols:

1. **NFS Procedures**: High-level operations like READ, WRITE, LOOKUP
2. **RPC (Remote Procedure Call)**: Framework for calling remote procedures
3. **XDR (eXternal Data Representation)**: Data encoding format
4. **Transport Layer**: Usually TCP or UDP
5. **Network Layer**: IP protocol

### RPC Programs

NFS relies on several RPC programs:

1. **MOUNT Protocol (Program 100005)**:
   - Provides initial file handle for mounting
   - Manages mount points
   - Authenticates mount requests

2. **NFS Protocol (Program 100003)**:
   - Handles actual file operations
   - Implements core functionality

3. **NLM Protocol (Program 100021)** (Optional):
   - Network Lock Manager
   - Provides file locking services

4. **NSM Protocol (Program 100024)** (Optional):
   - Network Status Monitor
   - Tracks server state for lock recovery

### Portmap/rpcbind

The portmap service (or rpcbind in newer systems) maps RPC program numbers to port numbers, allowing clients to find NFS services.

## NFSv3 Procedures

NFSv3 defines the following key procedures:

### File and Directory Operations

| Procedure    | Description                                       |
|--------------|---------------------------------------------------|
| NULL         | No-op, used for testing                           |
| GETATTR      | Get file attributes                               |
| SETATTR      | Set file attributes                               |
| LOOKUP       | Look up filename                                  |
| ACCESS       | Check access permissions                          |
| READLINK     | Read from symbolic link                           |
| READ         | Read from file                                    |
| WRITE        | Write to file                                     |
| CREATE       | Create a file                                     |
| MKDIR        | Create a directory                                |
| SYMLINK      | Create a symbolic link                            |
| MKNOD        | Create a special device                           |
| REMOVE       | Remove a file                                     |
| RMDIR        | Remove a directory                                |
| RENAME       | Rename a file or directory                        |
| LINK         | Create a hard link                                |
| READDIR      | Read from directory                               |
| READDIRPLUS  | Extended read from directory                      |
| FSSTAT       | Get filesystem statistics                         |
| FSINFO       | Get filesystem information                        |
| PATHCONF     | Get filesystem parameters                         |
| COMMIT       | Commit cached data to stable storage              |

## File Handles

File handles are opaque identifiers used by clients to reference files and directories on the server. Key characteristics of file handles:

1. **Opacity**: Clients treat them as opaque data blobs
2. **Persistence**: Should remain valid as long as the file exists
3. **Uniqueness**: Uniquely identify files and directories
4. **Security**: Should not be easily forgeable

In ABSNFS, file handles contain:
- A unique identifier for the file or directory
- A generation number to detect stale handles
- Security information to prevent forgery
- Additional metadata for efficient lookup

## Attributes

NFS defines a set of file attributes that can be queried and modified:

| Attribute    | Description                                   |
|--------------|-----------------------------------------------|
| type         | File type (regular, directory, symlink, etc.) |
| mode         | File permissions (Unix-style)                 |
| nlink        | Number of hard links                          |
| uid          | User ID of owner                              |
| gid          | Group ID                                      |
| size         | File size in bytes                            |
| used         | Space used by file                            |
| rdev         | Device IDs for special files                  |
| fsid         | Filesystem ID                                 |
| fileid       | File ID unique within filesystem              |
| atime        | Last access time                              |
| mtime        | Last modification time                        |
| ctime        | Last status change time                       |

## NFS Data Structures

### File Attributes (fattr3)

```
struct fattr3 {
    ftype3      type;       /* File type */
    mode3       mode;       /* Protection mode bits */
    uint32      nlink;      /* Number of hard links */
    uid3        uid;        /* User ID of owner */
    gid3        gid;        /* Group ID of owner */
    size3       size;       /* File size in bytes */
    size3       used;       /* Bytes actually used */
    specdata3   rdev;       /* Device ID */
    uint64      fsid;       /* Filesystem ID */
    fileid3     fileid;     /* File ID */
    nfstime3    atime;      /* Last access time */
    nfstime3    mtime;      /* Last modification time */
    nfstime3    ctime;      /* Last status change time */
};
```

### File System Statistics (fsstat3resok)

```
struct fsstat3resok {
    post_op_attr obj_attributes;
    size3        tbytes;     /* Total filesystem bytes */
    size3        fbytes;     /* Free filesystem bytes */
    size3        abytes;     /* Free bytes available to caller */
    size3        tfiles;     /* Total file slots */
    size3        ffiles;     /* Free file slots */
    size3        afiles;     /* Free file slots available to caller */
    uint32       invarsec;   /* Seconds for which this info is valid */
};
```

## NFS Protocol Flow

### Mount Process

1. Client contacts the server's portmap service to find the MOUNT program port
2. Client sends a MOUNT request for a specific export path
3. Server checks if the export exists and if the client has permission
4. Server returns a file handle for the export root
5. Client can now use this file handle for further NFS operations

### File Access Flow

1. Client uses LOOKUP to navigate from export root to desired file
2. Each successful LOOKUP returns a file handle for the found file/directory
3. Client uses this file handle in READ/WRITE operations
4. Server validates the file handle and performs requested operations
5. Results are returned to the client

### Write Example

1. Client issues WRITE request with file handle, offset, data
2. Server validates file handle
3. Server checks permissions
4. Server writes data to file
5. Server returns status and updated attributes

### Attribute Caching

To improve performance, clients can cache file attributes:
1. Server returns attributes with most operations
2. Attributes include a recommended cache validity period
3. Client can reuse cached attributes until they expire
4. Client must refresh attributes after expiration

## Error Handling

NFS operations return status codes indicating success or specific errors:

| Status Code       | Value | Description                           |
|-------------------|-------|---------------------------------------|
| NFS3_OK           | 0     | Success                               |
| NFS3ERR_PERM      | 1     | Not owner                             |
| NFS3ERR_NOENT     | 2     | No such file or directory             |
| NFS3ERR_IO        | 5     | I/O error                             |
| NFS3ERR_ACCES     | 13    | Permission denied                     |
| NFS3ERR_EXIST     | 17    | File exists                           |
| NFS3ERR_NOTDIR    | 20    | Not a directory                       |
| NFS3ERR_ISDIR     | 21    | Is a directory                        |
| NFS3ERR_NOSPC     | 28    | No space left on device               |
| NFS3ERR_ROFS      | 30    | Read-only file system                 |
| NFS3ERR_STALE     | 70    | Stale file handle                     |
| NFS3ERR_BADHANDLE | 10001 | Invalid file handle                   |
| NFS3ERR_SERVERFAULT | 10006 | Server fault (undefined error)      |

## Security Considerations

NFSv3 has limited security features:

1. **AUTH_NONE**: No authentication
2. **AUTH_SYS** (or AUTH_UNIX): Basic Unix-style UID/GID authentication
3. **AUTH_DH** (or AUTH_DES): DES-based authentication (rarely used)

NFSv3 security limitations:
- No encryption of data
- Limited authentication options
- Client IP addresses can be spoofed
- Root squashing as a basic security feature

## NFS Extensions and Features

Beyond the core protocol, NFS supports several extensions:

1. **WebNFS**: Simplified NFS access through firewalls and for web applications
2. **NFS-Ganesha**: User-space NFS server implementation
3. **pNFS** (Parallel NFS): Extension for parallel access to multiple servers
4. **NFS over RDMA**: Optimized performance using Remote Direct Memory Access

## ABSNFS Implementation Details

ABSNFS implements NFSv3 with these characteristics:

1. **Pure Go Implementation**: No external dependencies on system NFS libraries
2. **ABSFS Backend**: Uses the ABSFS interface for filesystem operations
3. **File Handle Mapping**: Maps between NFS file handles and ABSFS paths
4. **XDR Encoding/Decoding**: Implements XDR encoding/decoding in Go
5. **RPC Server**: Custom RPC server implementation

## References

For more detailed information about the NFS protocol:

1. [RFC 1813 - NFS Version 3 Protocol Specification](https://tools.ietf.org/html/rfc1813)
2. [RFC 1094 - NFS Version 2 Protocol Specification](https://tools.ietf.org/html/rfc1094)
3. [RFC 7530 - NFS Version 4 Protocol](https://tools.ietf.org/html/rfc7530)
4. [Linux NFS-HOWTO](https://nfs.sourceforge.net/nfs-howto/)
5. [NFS Illustrated](https://www.oreilly.com/library/view/nfs-illustrated/9780201325706/)