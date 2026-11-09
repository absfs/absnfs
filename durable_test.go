package absnfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/absfs/absfs"
	"github.com/absfs/basefs"
	"github.com/absfs/osfs"
)

// syncFS counts the backend's file opens, Syncs and Closes, and can fail
// Sync or Close. Its events record the order of opens, closes, removes and
// renames.
type syncFS struct {
	absfs.SymlinkFileSystem
	opens, syncs, closes atomic.Int64
	syncErr, closeErr    error
	noopSync             bool // Sync does nothing and succeeds

	mu     sync.Mutex
	events []string
}

func (f *syncFS) event(e string) {
	f.mu.Lock()
	f.events = append(f.events, e)
	f.mu.Unlock()
}

func (f *syncFS) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func (f *syncFS) OpenFile(name string, flag int, perm os.FileMode) (absfs.File, error) {
	file, err := f.SymlinkFileSystem.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	info, _ := file.Stat()
	if info == nil || info.IsDir() {
		return file, nil // READDIR's opens are not counted
	}
	f.opens.Add(1)
	f.event("open " + name)
	return &countedFile{File: file, fs: f, name: name}, nil
}

func (f *syncFS) Remove(name string) error {
	f.event("remove " + name)
	return f.SymlinkFileSystem.Remove(name)
}

func (f *syncFS) Rename(oldpath, newpath string) error {
	f.event("rename " + oldpath + " " + newpath)
	return f.SymlinkFileSystem.Rename(oldpath, newpath)
}

type countedFile struct {
	absfs.File
	fs   *syncFS
	name string
}

func (s *countedFile) Sync() error {
	s.fs.syncs.Add(1)
	if s.fs.syncErr != nil {
		return &os.PathError{Op: "sync", Path: s.name, Err: s.fs.syncErr}
	}
	if s.fs.noopSync {
		return nil
	}
	return s.File.Sync()
}

func (s *countedFile) Close() error {
	s.fs.closes.Add(1)
	s.fs.event("close " + s.name)
	err := s.File.Close()
	if s.fs.closeErr != nil {
		return s.fs.closeErr
	}
	return err
}

func writeBody(fh []byte, offset uint64, data []byte, stable uint32) []byte {
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fh)
	binary.Write(&buf, binary.BigEndian, offset)
	binary.Write(&buf, binary.BigEndian, uint32(len(data)))
	binary.Write(&buf, binary.BigEndian, stable)
	binary.Write(&buf, binary.BigEndian, uint32(len(data)))
	buf.Write(data)
	buf.Write(make([]byte, (4-len(data)%4)%4))
	return buf.Bytes()
}

// write sends a WRITE and returns its status, committed level and verifier.
func (e *fhEnv) write(fh []byte, offset uint64, data []byte, stable uint32) (st, committed uint32, verf [8]byte) {
	e.t.Helper()
	r, err := e.h.handleWrite(bytes.NewReader(writeBody(fh, offset, data, stable)), &RPCReply{}, e.auth)
	if err != nil {
		e.t.Fatal(err)
	}
	f := newFaReader(e.t, r)
	if st = f.status(); st != NFS_OK {
		return st, 0, verf
	}
	f.wcc()
	if n := f.u32(); n != uint32(len(data)) {
		e.t.Errorf("WRITE count %d, want %d", n, len(data))
	}
	committed = f.u32()
	binary.Read(f.r, binary.BigEndian, &verf)
	return st, committed, verf
}

// commit sends a COMMIT and returns its status and verifier.
func (e *fhEnv) commit(fh []byte) (uint32, [8]byte) {
	e.t.Helper()
	r, err := e.h.handleCommit(bytes.NewReader(buildCommitRequest(fh, 0, 0)), &RPCReply{}, e.auth)
	if err != nil {
		e.t.Fatal(err)
	}
	f := newFaReader(e.t, r)
	var verf [8]byte
	st := f.status()
	if st != NFS_OK {
		return st, verf
	}
	f.wcc()
	binary.Read(f.r, binary.BigEndian, &verf)
	return st, verf
}

func newSyncEnv(t *testing.T, opts ExportOptions) (*fhEnv, *syncFS) {
	t.Helper()
	sfs := &syncFS{SymlinkFileSystem: newMemFS(t)}
	writeFile(t, sfs.SymlinkFileSystem, "/f", 0)
	e := newFhEnv(t, sfs, opts)
	return e, sfs
}

func readBack(t *testing.T, fs absfs.SymlinkFileSystem, p string) string {
	t.Helper()
	if s, ok := fs.(*syncFS); ok {
		fs = s.SymlinkFileSystem // not counted
	}
	f, err := fs.OpenFile(p, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var buf bytes.Buffer
	buf.ReadFrom(f)
	return buf.String()
}

// TestUnstableWritesThenCommit: UNSTABLE WRITEs are answered UNSTABLE,
// share one open file and sync nothing; COMMIT syncs that file once and
// replies the verifier the WRITEs did.
func TestUnstableWritesThenCommit(t *testing.T) {
	e, sfs := newSyncEnv(t, ExportOptions{})
	fh := e.walk(e.mount("/"), "f")

	var first [8]byte
	for i := 0; i < 32; i++ {
		st, committed, verf := e.write(fh, uint64(i*4), []byte("abcd"), stableUnstable)
		if st != NFS_OK || committed != stableUnstable {
			t.Fatalf("WRITE %d: status %d committed %d, want NFS_OK and UNSTABLE", i, st, committed)
		}
		if i == 0 {
			first = verf
		} else if verf != first {
			t.Fatalf("WRITE %d: verifier changed", i)
		}
	}
	if first == ([8]byte{}) {
		t.Error("zero write verifier")
	}
	if o, s, c := sfs.opens.Load(), sfs.syncs.Load(), sfs.closes.Load(); o != 1 || s != 0 || c != 0 {
		t.Errorf("32 UNSTABLE WRITEs: %d opens, %d syncs, %d closes, want 1, 0, 0", o, s, c)
	}

	st, verf := e.commit(fh)
	if st != NFS_OK || verf != first {
		t.Errorf("COMMIT: status %d verifier %x, want NFS_OK and %x", st, verf, first)
	}
	if o, s := sfs.opens.Load(), sfs.syncs.Load(); o != 1 || s != 1 {
		t.Errorf("COMMIT: %d opens, %d syncs, want 1 and 1", o, s)
	}
	if got := readBack(t, sfs, "/f"); len(got) != 128 {
		t.Errorf("file holds %d bytes, want 128", len(got))
	}

	// READ uses the file WRITE holds.
	if data, err := e.nfs.Read(fileNode("/f"), 0, 4); err != nil || string(data) != "abcd" {
		t.Errorf("READ = %q, %v", data, err)
	}
	if o := sfs.opens.Load(); o != 1 {
		t.Errorf("READ opened the file again: %d opens", o)
	}
}

// TestStableWritesSync: FILE_SYNC and DATA_SYNC WRITEs sync before the
// reply and are answered FILE_SYNC.
func TestStableWritesSync(t *testing.T) {
	for _, stable := range []uint32{stableDataSync, stableFileSync} {
		e, sfs := newSyncEnv(t, ExportOptions{})
		fh := e.walk(e.mount("/"), "f")
		st, committed, _ := e.write(fh, 0, []byte("data"), stable)
		if st != NFS_OK || committed != stableFileSync {
			t.Errorf("stable %d: status %d committed %d, want NFS_OK and FILE_SYNC", stable, st, committed)
		}
		if s := sfs.syncs.Load(); s != 1 {
			t.Errorf("stable %d: %d syncs, want 1", stable, s)
		}
	}
}

// TestStableWriteSyncFailure: a FILE_SYNC WRITE whose Sync fails gets the
// error, and the verifier changes.
func TestStableWriteSyncFailure(t *testing.T) {
	e, sfs := newSyncEnv(t, ExportOptions{})
	fh := e.walk(e.mount("/"), "f")
	before := e.nfs.writeVerifier()
	sfs.syncErr = syscall.ENOSPC
	if st, _, _ := e.write(fh, 0, []byte("data"), stableFileSync); st != NFSERR_NOSPC {
		t.Errorf("WRITE status %d, want NFSERR_NOSPC", st)
	}
	if e.nfs.writeVerifier() == before {
		t.Error("verifier unchanged after a failed sync")
	}
}

// TestCommitSyncFailure: COMMIT never answers NFS3_OK without syncing. A
// failed Sync is answered with its error, and the verifier changes, so the
// client sends its unstable data again.
func TestCommitSyncFailure(t *testing.T) {
	e, sfs := newSyncEnv(t, ExportOptions{})
	fh := e.walk(e.mount("/"), "f")
	_, _, verf := e.write(fh, 0, []byte("data"), stableUnstable)
	sfs.syncErr = syscall.EIO
	if st, _ := e.commit(fh); st != NFSERR_IO {
		t.Errorf("COMMIT status %d, want NFSERR_IO", st)
	}
	sfs.syncErr = nil
	st, after := e.commit(fh)
	if st != NFS_OK || after == verf {
		t.Errorf("COMMIT after a failed one: status %d, verifier %x (was %x), want NFS_OK and a new verifier", st, after, verf)
	}
}

// TestCommitOpensWhenNoneHeld: COMMIT of a file no longer held (closed
// idle, or at a restart of the table) opens it and syncs it.
func TestCommitOpensWhenNoneHeld(t *testing.T) {
	e, sfs := newSyncEnv(t, ExportOptions{})
	fh := e.walk(e.mount("/"), "f")
	e.write(fh, 0, []byte("data"), stableUnstable)
	e.nfs.files.release("/")
	if c := sfs.closes.Load(); c != 1 {
		t.Fatalf("release: %d closes, want 1", c)
	}
	if st, _ := e.commit(fh); st != NFS_OK {
		t.Errorf("COMMIT status %d", st)
	}
	if o, s := sfs.opens.Load(), sfs.syncs.Load(); o != 2 || s != 1 {
		t.Errorf("COMMIT: %d opens, %d syncs, want 2 and 1", o, s)
	}
}

// TestCommitOfDirectory: COMMIT of a directory syncs nothing and says so.
func TestCommitOfDirectory(t *testing.T) {
	e, sfs := newSyncEnv(t, ExportOptions{})
	mustMkdir(t, sfs, "/d")
	d := e.walk(e.mount("/"), "d")
	if st, _ := e.commit(d); st != NFSERR_ISDIR {
		t.Errorf("COMMIT of a directory: status %d, want NFSERR_ISDIR", st)
	}
}

// TestNoopSyncBackend: a backend whose Sync does nothing still serves
// WRITE and COMMIT.
func TestNoopSyncBackend(t *testing.T) {
	e, sfs := newSyncEnv(t, ExportOptions{})
	sfs.noopSync = true
	fh := e.walk(e.mount("/"), "f")
	if st, c, _ := e.write(fh, 0, []byte("data"), stableFileSync); st != NFS_OK || c != stableFileSync {
		t.Errorf("FILE_SYNC WRITE: status %d committed %d", st, c)
	}
	if st, _, _ := e.write(fh, 4, []byte("more"), stableUnstable); st != NFS_OK {
		t.Errorf("UNSTABLE WRITE: status %d", st)
	}
	if st, _ := e.commit(fh); st != NFS_OK {
		t.Errorf("COMMIT: status %d", st)
	}
	if got := readBack(t, sfs, "/f"); got != "datamore" {
		t.Errorf("file holds %q", got)
	}
}

// TestWriteVerifierOption: the verifier is ExportOptions.WriteVerifier,
// changeable at runtime, and each server picks its own by default.
func TestWriteVerifierOption(t *testing.T) {
	v1 := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	e, _ := newSyncEnv(t, ExportOptions{WriteVerifier: v1})
	fh := e.walk(e.mount("/"), "f")
	if _, _, verf := e.write(fh, 0, []byte("x"), stableUnstable); verf != v1 {
		t.Errorf("WRITE verifier %x, want %x", verf, v1)
	}
	if got := e.nfs.GetExportOptions().WriteVerifier; got != v1 {
		t.Errorf("GetExportOptions().WriteVerifier = %x", got)
	}

	v2 := [8]byte{9, 9, 9, 9, 9, 9, 9, 9}
	opts := e.nfs.GetExportOptions()
	opts.WriteVerifier = v2
	if err := e.nfs.UpdateExportOptions(opts); err != nil {
		t.Fatal(err)
	}
	if _, verf := e.commit(fh); verf != v2 {
		t.Errorf("COMMIT verifier after update %x, want %x", verf, v2)
	}
	// Zero in an update leaves it as it is.
	e.nfs.UpdateTuningOptions(func(t *TuningOptions) { t.WriteVerifier = [8]byte{} })
	if got := e.nfs.writeVerifier(); got != v2 {
		t.Errorf("a zero update changed the verifier to %x", got)
	}

	a, _ := newSyncEnv(t, ExportOptions{})
	b, _ := newSyncEnv(t, ExportOptions{})
	if a.nfs.writeVerifier() == b.nfs.writeVerifier() || a.nfs.writeVerifier() == ([8]byte{}) {
		t.Error("two servers share a default verifier, or it is zero")
	}
}

// TestHeldFileIdleClose: a held file is closed once idle.
func TestHeldFileIdleClose(t *testing.T) {
	defer func(d time.Duration) { openFileIdle = d }(openFileIdle)
	openFileIdle = 20 * time.Millisecond
	e, sfs := newSyncEnv(t, ExportOptions{})
	fh := e.walk(e.mount("/"), "f")
	e.write(fh, 0, []byte("x"), stableUnstable)
	deadline := time.Now().Add(5 * time.Second)
	for e.nfs.files.count() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n, c := e.nfs.files.count(), sfs.closes.Load(); n != 0 || c != 1 {
		t.Errorf("after idling: %d held, %d closes, want 0 and 1", n, c)
	}
}

// TestHeldFilesBounded: no more than maxOpenFiles files are held; the
// least recently used is closed first.
func TestHeldFilesBounded(t *testing.T) {
	defer func(n int) { maxOpenFiles = n }(maxOpenFiles)
	maxOpenFiles = 2
	e, sfs := newSyncEnv(t, ExportOptions{})
	root := e.mount("/")
	for _, name := range []string{"a", "b", "c"} {
		writeFile(t, sfs.SymlinkFileSystem, "/"+name, 0)
		fh := e.walk(root, name)
		if st, _, _ := e.write(fh, 0, []byte(name), stableUnstable); st != NFS_OK {
			t.Fatalf("WRITE %s: %d", name, st)
		}
	}
	if n, c := e.nfs.files.count(), sfs.closes.Load(); n != 2 || c != 1 {
		t.Errorf("%d held, %d closes, want 2 and 1", n, c)
	}
	if log := sfs.log(); log[len(log)-1] != "close /a" {
		t.Errorf("last event %q, want the least recently used closed", log[len(log)-1])
	}
}

// TestHeldFileClosedBeforeRemoveAndRename: the server closes a held file
// before it removes or renames its path (Windows refuses either on an open
// file), and a new file under a renamed file's old name is not written
// through the old one's file.
func TestHeldFileClosedBeforeRemoveAndRename(t *testing.T) {
	e, sfs := newSyncEnv(t, ExportOptions{})
	root := e.mount("/")
	writeFile(t, sfs.SymlinkFileSystem, "/g", 0)
	f, g := e.walk(root, "f"), e.walk(root, "g")
	e.write(f, 0, []byte("old"), stableUnstable)
	e.write(g, 0, []byte("gone"), stableUnstable)

	if st := e.remove(root, "g"); st != NFS_OK {
		t.Fatalf("REMOVE: %d", st)
	}
	if st := e.rename(root, "f", root, "moved"); st != NFS_OK {
		t.Fatalf("RENAME: %d", st)
	}
	log := sfs.log()
	index := func(ev string) int {
		for i, x := range log {
			if x == ev {
				return i
			}
		}
		return -1
	}
	if c, r := index("close /g"), index("remove /g"); c < 0 || c > r {
		t.Errorf("events %v: /g not closed before its REMOVE", log)
	}
	if c, r := index("close /f"), index("rename /f /moved"); c < 0 || c > r {
		t.Errorf("events %v: /f not closed before its RENAME", log)
	}

	writeFile(t, sfs.SymlinkFileSystem, "/f", 0)
	e.write(e.walk(root, "f"), 0, []byte("new"), stableUnstable)
	e.write(f, 3, []byte("+"), stableUnstable) // the old handle follows the rename
	if got := readBack(t, sfs, "/f"); got != "new" {
		t.Errorf("/f holds %q, want \"new\"", got)
	}
	if got := readBack(t, sfs, "/moved"); got != "old+" {
		t.Errorf("/moved holds %q, want \"old+\"", got)
	}
}

// TestHeldFileReplacedBehindServer: a file replaced on the host (an
// editor's save by rename) is not written through the file held for the
// old one.
func TestHeldFileReplacedBehindServer(t *testing.T) {
	dir := t.TempDir()
	ofs, err := osfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	bfs, err := basefs.NewFS(ofs, filepath.ToSlash(dir))
	if err != nil {
		t.Skipf("basefs over %s: %v", dir, err)
	}
	host := func(name string) string { return filepath.Join(dir, name) }
	if err := os.WriteFile(host("f"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e := newFhEnv(t, bfs, ExportOptions{})
	fh := e.walk(e.mount("/"), "f")
	if st, _, _ := e.write(fh, 0, []byte("one"), stableUnstable); st != NFS_OK {
		t.Fatalf("WRITE: %d", st)
	}
	e.nfs.files.mu.Lock()
	held := len(e.nfs.files.files)
	e.nfs.files.mu.Unlock()
	if held != 1 {
		t.Fatalf("%d files held", held)
	}

	if err := os.WriteFile(host("new"), []byte("xyz"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(host("new"), host("f")); err != nil {
		t.Skipf("cannot replace an open file here: %v", err)
	}
	e.nfs.attrCache.Clear()
	if st, _, _ := e.write(fh, 3, []byte("two"), stableUnstable); st != NFS_OK {
		t.Fatalf("WRITE: %d", st)
	}
	e.nfs.files.release("/")
	got, err := os.ReadFile(host("f"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "xyztwo" {
		t.Errorf("f holds %q, want \"xyztwo\"", got)
	}
}

// TestFailedCloseOfDirtyFileChangesVerifier: a held file written since its
// last Sync whose Close fails may have lost data, so the verifier changes.
func TestFailedCloseOfDirtyFileChangesVerifier(t *testing.T) {
	e, sfs := newSyncEnv(t, ExportOptions{})
	fh := e.walk(e.mount("/"), "f")
	_, _, verf := e.write(fh, 0, []byte("x"), stableUnstable)
	sfs.closeErr = syscall.EIO
	e.nfs.files.release("/")
	if e.nfs.writeVerifier() == verf {
		t.Error("verifier unchanged after a failed close of unsynced data")
	}

	// A clean file's failed close loses nothing.
	sfs.closeErr = nil
	e.write(fh, 0, []byte("x"), stableUnstable)
	e.commit(fh)
	v := e.nfs.writeVerifier()
	sfs.closeErr = syscall.EIO
	e.nfs.files.release("/")
	if e.nfs.writeVerifier() != v {
		t.Error("verifier changed after a failed close of synced data")
	}
}

// TestParallelWritesShareOneFile: concurrent WRITEs to one file open it
// once.
func TestParallelWritesShareOneFile(t *testing.T) {
	// A host directory: memfs races Lstat against WriteAt by itself.
	ofs, err := osfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	bfs, err := basefs.NewFS(ofs, filepath.ToSlash(t.TempDir()))
	if err != nil {
		t.Skipf("basefs: %v", err)
	}
	sfs := &syncFS{SymlinkFileSystem: bfs}
	writeFile(t, bfs, "/f", 0)
	e := newFhEnv(t, sfs, ExportOptions{})
	fh := e.walk(e.mount("/"), "f")
	var wg sync.WaitGroup
	var failed atomic.Int64
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := e.h.handleWrite(bytes.NewReader(writeBody(fh, uint64(i), []byte{'a' + byte(i)}, stableUnstable)), &RPCReply{}, e.auth)
			if err != nil || readStatusFromReply(r) != NFS_OK {
				failed.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if failed.Load() != 0 {
		t.Errorf("%d WRITEs failed", failed.Load())
	}
	if o := sfs.opens.Load(); o != 1 {
		t.Errorf("%d opens for 16 parallel WRITEs, want 1", o)
	}
	if n := e.nfs.files.count(); n != 1 {
		t.Errorf("%d files held, want 1", n)
	}
	if got := readBack(t, sfs, "/f"); got != "abcdefghijklmnop" {
		t.Errorf("file holds %q", got)
	}
}

// TestCloseReleasesHeldFiles: Close and Unexport close every held file.
func TestCloseReleasesHeldFiles(t *testing.T) {
	e, sfs := newSyncEnv(t, ExportOptions{})
	fh := e.walk(e.mount("/"), "f")
	e.write(fh, 0, []byte("x"), stableUnstable)
	if err := e.nfs.Unexport(); err != nil {
		t.Fatal(err)
	}
	if c := sfs.closes.Load(); c != 1 {
		t.Errorf("Unexport: %d closes, want 1", c)
	}
	e.write(fh, 0, []byte("y"), stableUnstable)
	if err := e.nfs.Close(); err != nil {
		t.Fatal(err)
	}
	if c := sfs.closes.Load(); c != 2 {
		t.Errorf("Close: %d closes, want 2", c)
	}
}

// errorsIsBusy guards errFileBusy's mapping.
func TestErrFileBusyIsJukebox(t *testing.T) {
	if mapError(errFileBusy) != NFSERR_DELAY || !errors.Is(errFileBusy, errFileBusy.errno) {
		t.Error("errFileBusy does not map to NFS3ERR_JUKEBOX")
	}
}
