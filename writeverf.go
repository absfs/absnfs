// writeverf.go: the write verifier WRITE and COMMIT replies carry.
package absnfs

// writeVerf is the write verifier (RFC 1813 writeverf3) of every WRITE and
// COMMIT reply, from every server, across restarts. It never changes.
//
// A verifier that changes between an UNSTABLE WRITE and its COMMIT makes
// the client resend what it wrote, which RFC 1813 intends. The macOS
// client mishandles it: after a COMMIT answered NFS3_OK with another
// verifier it leaves a dirty buffer on its delayed-write queue without the
// flag saying so, and a later unmount, of any NFS mount, panics the kernel
// removing it from the wrong global queue ("corrupt list around element"
// in nfs_buf_remfree; seen on 2026-11-09). So absnfs never asks a client to
// hold data for a COMMIT: every WRITE is synced before its reply and
// answered FILE_SYNC, a failed sync is answered with its error, and the
// verifier has nothing to signal.
var writeVerf = [8]byte{'a', 'b', 's', 'n', 'f', 's', 0, 1}

// writeVerifier returns writeVerf.
func (s *AbsfsNFS) writeVerifier() [8]byte {
	return writeVerf
}
