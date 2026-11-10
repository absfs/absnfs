// openfiles.go: the backend files the server keeps open between requests.
//
// READ, WRITE and COMMIT use one open file per path, held across requests,
// rather than opening and closing the file for each one. A backend that
// treats Close as a commit point would otherwise commit once per WRITE, and
// COMMIT needs a file to Sync. Held files are closed when idle, when there
// are too many, before the server removes or renames their path (Windows
// refuses to remove or rename an open file), and at Unexport and Close.
package absnfs

import (
	"container/list"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/absfs/absfs"
)

// openFileIdle is how long a held file stays open with no request using it.
// A variable for tests.
var openFileIdle = 5 * time.Second

// maxOpenFiles bounds the files held at once; the least recently used is
// closed first. It stays well below macOS's default limit of 256 open files
// for a process started from a shell. A variable for tests.
var maxOpenFiles = 64

// errFileBusy answers a request whose held file was closed by another
// request (a RENAME, a REMOVE, Unexport) as soon as it was opened. The
// client retries (NFS3ERR_JUKEBOX).
var errFileBusy = &statusError{NFSERR_DELAY, "file closed while in use, retry", errors.New("file closed while in use")}

// fileAccess is what a held file was opened for.
type fileAccess uint8

const (
	accessRead fileAccess = 1 << iota
	accessWrite
)

// heldFile is a backend file held open for a path.
type heldFile struct {
	mu     sync.Mutex // serializes I/O on f and its Close
	f      absfs.File
	path   string
	access fileAccess
	closed bool

	// Guarded by openFiles.mu.
	lastUse time.Time
	elem    *list.Element
}

// openFiles is the table of held files, by node path.
type openFiles struct {
	mu    sync.Mutex
	files map[string]*heldFile
	lru   list.List // of *heldFile, most recently used first
	timer *time.Timer

	// opening has an entry for each path a request is opening, closed when
	// the file is held (or the open failed): the parallel WRITEs a client
	// sends when it starts writing a file wait and share that file rather
	// than each opening one.
	opening map[string]chan struct{}

	// closeFailed is told of a Close that failed. Every WRITE was synced
	// before its reply, so no client is waiting on what the file held.
	closeFailed func(path string, err error)
}

func newOpenFiles(closeFailed func(string, error)) *openFiles {
	return &openFiles{
		files:       make(map[string]*heldFile),
		opening:     make(map[string]chan struct{}),
		closeFailed: closeFailed,
	}
}

// openFunc opens the file at a path for a request, reporting what for.
type openFunc func() (absfs.File, fileAccess, error)

// use runs fn with the file held for p, opened for at least need, locked
// against other requests. A held file that is not the object current
// describes (replaced behind the server's back, where the backend can
// tell) is closed and the path opened again. current may be nil.
func (t *openFiles) use(p string, need fileAccess, current os.FileInfo, open openFunc, fn func(*heldFile) error) error {
	// A file held a moment ago can be closed before it is locked (a RENAME
	// or REMOVE released it, another request replaced it with one opened
	// for more), so the path is tried a few times.
	for attempt := 0; attempt < 4; attempt++ {
		h, wait := t.acquire(p, need)
		if wait != nil {
			<-wait
			continue
		}
		if h != nil {
			if heldIs(h, current) {
				defer h.mu.Unlock()
				return fn(h)
			}
			h.mu.Unlock()
			t.drop(h)
			continue
		}
		// This request opens p; others wait for it.
		f, access, err := open()
		if err != nil {
			t.opened(p)
			return err
		}
		h = t.hold(p, f, access)
		h.mu.Lock()
		if !h.closed {
			defer h.mu.Unlock()
			return fn(h)
		}
		h.mu.Unlock()
	}
	return errFileBusy
}

// sync syncs h, which the caller has locked. A failed Sync is the
// caller's error to answer; it never changes the write verifier (see
// writeVerf).
func (t *openFiles) sync(h *heldFile) error {
	if err := h.f.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", h.path, err)
	}
	return nil
}

// heldIs reports whether h's file is the object current describes, or the
// backend cannot tell.
func heldIs(h *heldFile, current os.FileInfo) bool {
	if current == nil {
		return true
	}
	info, err := h.f.Stat()
	if err != nil {
		return false
	}
	same, known := sameFile(info, current)
	return same || !known
}

// acquire returns the file held for p, if it was opened for need, locked
// and open. Otherwise, while another request is opening p, it returns a
// channel closed when that is done; else neither, and the caller is to
// open p, then hold the file or call opened.
func (t *openFiles) acquire(p string, need fileAccess) (*heldFile, chan struct{}) {
	for {
		t.mu.Lock()
		h := t.files[p]
		if h == nil || h.access&need != need {
			if wait := t.opening[p]; wait != nil {
				t.mu.Unlock()
				return nil, wait
			}
			t.opening[p] = make(chan struct{})
			t.mu.Unlock()
			return nil, nil
		}
		h.lastUse = time.Now()
		t.lru.MoveToFront(h.elem)
		t.mu.Unlock()

		h.mu.Lock()
		if !h.closed {
			return h, nil
		}
		// Closed since: it was detached first, so look again.
		h.mu.Unlock()
	}
}

// opened ends this request's opening of p. t.mu is not held.
func (t *openFiles) opened(p string) {
	t.mu.Lock()
	t.endOpening(p)
	t.mu.Unlock()
}

// endOpening wakes the requests waiting for p to be opened. t.mu is held.
func (t *openFiles) endOpening(p string) {
	if wait := t.opening[p]; wait != nil {
		delete(t.opening, p)
		close(wait)
	}
}

// hold holds f for p, closing the file held for p before, if any, and the
// least recently used files beyond maxOpenFiles, and ends the opening of p.
func (t *openFiles) hold(p string, f absfs.File, access fileAccess) *heldFile {
	h := &heldFile{f: f, path: p, access: access, lastUse: time.Now()}
	var victims []*heldFile
	t.mu.Lock()
	if old := t.files[p]; old != nil {
		t.detach(old)
		victims = append(victims, old)
	}
	t.files[p] = h
	h.elem = t.lru.PushFront(h)
	for len(t.files) > maxOpenFiles {
		v := t.lru.Back().Value.(*heldFile)
		t.detach(v)
		victims = append(victims, v)
	}
	if t.timer == nil {
		t.timer = time.AfterFunc(openFileIdle, t.sweep)
	}
	t.endOpening(p)
	t.mu.Unlock()
	t.closeAll(victims)
	return h
}

// detach removes h from the table. t.mu is held.
func (t *openFiles) detach(h *heldFile) {
	if t.files[h.path] == h {
		delete(t.files, h.path)
	}
	if h.elem != nil {
		t.lru.Remove(h.elem)
		h.elem = nil
	}
}

// drop detaches h, if it is still held, and closes it.
func (t *openFiles) drop(h *heldFile) {
	t.mu.Lock()
	t.detach(h)
	t.mu.Unlock()
	t.close(h)
}

// close closes h once any request using it is done. A failed Close is
// reported to closeFailed.
func (t *openFiles) close(h *heldFile) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	if err := h.f.Close(); err != nil && t.closeFailed != nil {
		t.closeFailed(h.path, err)
	}
}

func (t *openFiles) closeAll(hs []*heldFile) {
	for _, h := range hs {
		t.close(h)
	}
}

// sweep closes the files idle for openFileIdle, and runs again while any
// file is held.
func (t *openFiles) sweep() {
	var victims []*heldFile
	t.mu.Lock()
	cutoff := time.Now().Add(-openFileIdle)
	for e := t.lru.Back(); e != nil; {
		h := e.Value.(*heldFile)
		if h.lastUse.After(cutoff) {
			break
		}
		e = e.Prev()
		t.detach(h)
		victims = append(victims, h)
	}
	// Run again once the least recently used file has been idle long enough.
	if e := t.lru.Back(); e != nil {
		t.timer = time.AfterFunc(e.Value.(*heldFile).lastUse.Sub(cutoff), t.sweep)
	} else {
		t.timer = nil
	}
	t.mu.Unlock()
	t.closeAll(victims)
}

// release closes the files held for each of roots and everything below
// them, matching names without regard to case (closing a file held under
// another spelling only costs a reopen). It waits for requests using them.
// "/" releases every file.
func (t *openFiles) release(roots ...string) {
	var victims []*heldFile
	t.mu.Lock()
	for p, h := range t.files {
		for _, root := range roots {
			if within(p, root) {
				t.detach(h)
				victims = append(victims, h)
				break
			}
		}
	}
	if len(t.files) == 0 && t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.mu.Unlock()
	t.closeAll(victims)
}

// within reports whether p is root or below it, ignoring case.
func within(p, root string) bool {
	if root == "/" || strings.EqualFold(p, root) {
		return true
	}
	return len(p) > len(root) && p[len(root)] == '/' && strings.EqualFold(p[:len(root)], root)
}

// count returns the number of files held.
func (t *openFiles) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.files)
}
