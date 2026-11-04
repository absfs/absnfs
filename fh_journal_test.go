package absnfs

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/absfs/absfs"
)

// restartEnv serves the same filesystem with one server after another, as
// a process that restarts does.
type restartEnv struct {
	t   *testing.T
	fs  absfs.SymlinkFileSystem
	dir string
}

func (r *restartEnv) start(opts ExportOptions) *fhEnv {
	r.t.Helper()
	opts.HandleStateDir = r.dir
	nfs, err := New(r.fs, opts)
	if err != nil {
		r.t.Fatal(err)
	}
	return &fhEnv{
		t: r.t, fs: r.fs, nfs: nfs,
		h: &NFSProcedureHandler{server: &Server{handler: nfs, options: ServerOptions{}}},
		auth: &AuthContext{
			ClientIP: "127.0.0.1", ClientPort: 1023,
			Credential:   &RPCCredential{Flavor: AUTH_SYS},
			EffectiveUID: nfs.owner.UID, EffectiveGID: nfs.owner.GID,
		},
	}
}

func TestHandlesSurviveRestartWithStateDir(t *testing.T) {
	fs := newMemFS(t)
	mustMkdir(t, fs, "/repo/.git")
	writeFile(t, fs, "/repo/.git/index", 10)
	writeFile(t, fs, "/repo/.git/index.lock", 20)
	writeFile(t, fs, "/repo/gone", 30)
	writeFile(t, fs, "/repo/"+strings.Repeat("long_name_", 8), 40)
	r := &restartEnv{t: t, fs: fs, dir: filepath.Join(t.TempDir(), "state")}

	e := r.start(ExportOptions{})
	root := e.mount("/")
	git := e.walk(root, "repo/.git")
	repo := e.walk(root, "repo")
	index, lock := e.walk(root, "repo/.git/index"), e.walk(root, "repo/.git/index.lock")
	gone := e.walk(root, "repo/gone")
	long := e.walk(root, "repo/"+strings.Repeat("long_name_", 8))
	if st := e.rename(git, "index", git, ".nfs01"); st != NFS_OK {
		t.Fatal(st)
	}
	if st := e.rename(git, "index.lock", git, "index"); st != NFS_OK {
		t.Fatal(st)
	}
	if st := e.remove(repo, "gone"); st != NFS_OK {
		t.Fatal(st)
	}
	writeFile(t, fs, "/repo/gone", 31)
	if err := e.nfs.Close(); err != nil {
		t.Fatal(err)
	}

	e = r.start(ExportOptions{})
	defer e.nfs.Close()
	if again := e.mount("/"); !bytes.Equal(again, root) {
		t.Errorf("the root's handle changed across the restart")
	}
	e.size(index, 10, "the open index after the restart")
	e.size(lock, 20, "index.lock after the restart")
	e.size(long, 40, "a tail handle after the restart")
	e.stale(gone, "a removed and recreated file after the restart")
	if newIndex := e.walk(root, "repo/.git/index"); bytes.Equal(newIndex, index) {
		t.Error("the new index has the old index's handle after the restart")
	}
	// The clock carries on above the journal's epochs.
	e.nfs.fileMap.log.mu.Lock()
	last := e.nfs.fileMap.log.last
	e2 := e.nfs.fileMap.log.nextEpoch()
	e.nfs.fileMap.log.mu.Unlock()
	if e2 <= last || last == 0 {
		t.Errorf("epoch after restart %d, journal's last %d", e2, last)
	}
}

func TestHandlesStaleAfterRestartWithoutStateDir(t *testing.T) {
	fs := newMemFS(t)
	writeFile(t, fs, "/f", 1)
	e := newFhEnv(t, fs, ExportOptions{})
	f := e.walk(e.mount("/"), "f")
	e2 := newFhEnv(t, fs, ExportOptions{})
	e2.stale(f, "a handle from before the restart")
}

func TestHandleStateDirIsExclusive(t *testing.T) {
	fs := newMemFS(t)
	dir := t.TempDir()
	one, err := New(fs, ExportOptions{HandleStateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(fs, ExportOptions{HandleStateDir: dir}); err == nil {
		t.Error("a second server shares the state directory")
	}
	if err := one.Close(); err != nil {
		t.Fatal(err)
	}
	if err := one.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	two, err := New(fs, ExportOptions{HandleStateDir: dir})
	if err != nil {
		t.Fatalf("after Close the directory is still locked: %v", err)
	}
	two.Close()
}

func TestHandleStateDirDamage(t *testing.T) {
	fs := newMemFS(t)
	writeFile(t, fs, "/f", 1)
	r := &restartEnv{t: t, fs: fs, dir: t.TempDir()}
	e := r.start(ExportOptions{})
	f := e.walk(e.mount("/"), "f")
	e.nfs.Close()

	// A key of the wrong size is an error, not a silent new key.
	keyPath := filepath.Join(r.dir, handleKeyFile)
	key, _ := os.ReadFile(keyPath)
	os.WriteFile(keyPath, key[:10], 0o600)
	if _, err := New(fs, ExportOptions{HandleStateDir: r.dir}); err == nil {
		t.Error("a 10-byte key was accepted")
	}
	os.WriteFile(keyPath, key, 0o600)

	// A key without its log is never trusted: both start afresh.
	os.Remove(filepath.Join(r.dir, handleLogFile))
	e = r.start(ExportOptions{})
	e.stale(f, "a handle whose journal was deleted")
	e.nfs.Close()
	if again, _ := os.ReadFile(keyPath); bytes.Equal(again, key) {
		t.Error("the key was kept without its journal")
	}

	// Unreadable state is an error.
	if err := os.WriteFile(filepath.Join(t.TempDir(), "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(fs, ExportOptions{HandleStateDir: filepath.Join(r.dir, handleKeyFile, "sub")}); err == nil {
		t.Error("a state directory below a file was accepted")
	}
}

// A record torn by a crash ends the journal; the records before it hold.
func TestHandleJournalTornRecord(t *testing.T) {
	fs := newMemFS(t)
	mustMkdir(t, fs, "/d")
	writeFile(t, fs, "/d/a", 1)
	writeFile(t, fs, "/d/b", 2)
	r := &restartEnv{t: t, fs: fs, dir: t.TempDir()}
	e := r.start(ExportOptions{})
	root := e.mount("/")
	d, a, b := e.walk(root, "d"), e.walk(root, "d/a"), e.walk(root, "d/b")
	if st := e.remove(d, "a"); st != NFS_OK {
		t.Fatal(st)
	}
	e.nfs.Close()
	logPath := filepath.Join(r.dir, handleLogFile)
	good, _ := os.ReadFile(logPath)
	torn := append(append([]byte(nil), good...), appendRecord(nil, tombPayload(1, 2))[:5]...)
	os.WriteFile(logPath, torn, 0o600)
	writeFile(t, fs, "/d/a", 3)

	e = r.start(ExportOptions{})
	defer e.nfs.Close()
	e.stale(a, "removed before the torn record")
	e.size(b, 2, "untouched")
	if st, _ := os.Stat(logPath); st.Size() != int64(len(good)) {
		t.Errorf("journal of %d bytes after load, want the %d good ones", st.Size(), len(good))
	}
	// Corrupt records (bad checksum, unknown type) end it the same way.
	l := testLog()
	bad := appendRecord(nil, tombPayload(1, 2))
	bad[len(bad)-1] ^= 1
	if n := l.replay(bad); n != 0 {
		t.Errorf("a record with a bad checksum replayed (%d bytes)", n)
	}
	if n := l.replay(appendRecord(nil, []byte{'X', 1, 2})); n != 0 {
		t.Error("an unknown record replayed")
	}
	if n := l.replay(appendRecord(nil, []byte{recRename, 1})); n != 0 {
		t.Error("a short rename record replayed")
	}
}

// The journal is compacted once it outgrows its live records; what it
// records survives the compaction and a restart.
func TestHandleJournalCompaction(t *testing.T) {
	defer func(n int64) { journalSlack = n }(journalSlack)
	journalSlack = 256
	defer func(a, b int) { tombLimit, coarseSlots = a, b }(tombLimit, coarseSlots)
	tombLimit, coarseSlots = 16, 1<<12

	fs := newMemFS(t)
	mustMkdir(t, fs, "/d")
	writeFile(t, fs, "/d/moved", 7)
	r := &restartEnv{t: t, fs: fs, dir: t.TempDir()}
	// A fixed key, so which paths share coarse slots is the same each run
	// (with this one, /d shares none with the removed files).
	os.WriteFile(filepath.Join(r.dir, handleKeyFile), bytes.Repeat([]byte{3}, handleKeySize), 0o600)
	os.WriteFile(filepath.Join(r.dir, handleLogFile), nil, 0o600)
	e := r.start(ExportOptions{})
	root := e.mount("/")
	d := e.walk(root, "d")
	moved := e.walk(root, "d/moved")
	if st := e.rename(d, "moved", d, "there"); st != NFS_OK {
		t.Fatal(st)
	}
	var removed [][]byte
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("f%d", i)
		writeFile(t, fs, "/d/"+name, 100+i)
		removed = append(removed, e.walk(root, "d/"+name))
		if st := e.remove(d, name); st != NFS_OK {
			t.Fatal(st)
		}
	}
	j := e.nfs.fileMap.log.journal
	j.mu.Lock()
	compacted := j.base > 0
	j.mu.Unlock()
	if !compacted {
		t.Fatal("the journal was never compacted")
	}
	if e.nfs.fileMap.log.folds.Load() == 0 {
		t.Fatal("no fold: the coarse snapshot is not exercised")
	}
	e.nfs.Close()
	for i := 0; i < 200; i++ {
		writeFile(t, fs, fmt.Sprintf("/d/f%d", i), 1000+i)
	}

	e = r.start(ExportOptions{})
	defer e.nfs.Close()
	e.size(moved, 7, "renamed before compaction")
	for i, fh := range removed {
		e.stale(fh, fmt.Sprintf("f%d removed before compaction", i))
	}
}

// A journal that cannot be written is abandoned and its key removed: the
// next start makes a new key, so no handle is trusted with records missing.
func TestHandleJournalWriteFailure(t *testing.T) {
	fs := newMemFS(t)
	mustMkdir(t, fs, "/d")
	writeFile(t, fs, "/d/a", 1)
	writeFile(t, fs, "/d/b", 2)
	dir := t.TempDir()
	var failed error
	fm, err := newFileHandleMapWithState(0, dir, "", func(err error) { failed = err })
	if err != nil {
		t.Fatal(err)
	}
	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer nfs.Close()
	nfs.fileMap = fm
	j := fm.log.journal
	j.f.Close() // writes now fail
	if err := nfs.Remove(nfs.root, "d"); err == nil {
		t.Fatal("removed a non-empty directory")
	}
	dnode, _ := nfs.Lookup("/d")
	if err := nfs.Remove(dnode, "a"); err != nil {
		t.Fatal(err)
	}
	if failed == nil {
		t.Error("onFail was not called")
	}
	if _, err := os.Stat(filepath.Join(dir, handleKeyFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the key survives a failed journal: %v", err)
	}
	// The running server keeps its log in memory and stays correct.
	if _, _, ok := fm.log.validate(context.Background(), "d/a", 0); ok {
		t.Error("the removed file's handle is valid")
	}
	j.write([]byte("ignored")) // abandoned: nothing happens
	j.sync()
	j.lock.Close()
}

func TestHandleOptions(t *testing.T) {
	fs := newMemFS(t)
	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer nfs.Close()
	if got := nfs.GetExportOptions().MaxHandles; got != DefaultMaxHandles {
		t.Errorf("default MaxHandles %d, want %d", got, DefaultMaxHandles)
	}
	for i := 0; i < 50; i++ {
		nfs.fileMap.issue(fileNode(fmt.Sprintf("/f%d", i)))
	}
	opts := nfs.GetExportOptions()
	opts.MaxHandles = 20
	if err := nfs.UpdateExportOptions(opts); err != nil {
		t.Fatal(err)
	}
	if nfs.fileMap.limit() != 20 || nfs.fileMap.Count() > 20 || nfs.GetExportOptions().MaxHandles != 20 {
		t.Errorf("after MaxHandles 20: limit %d, count %d, option %d", nfs.fileMap.limit(), nfs.fileMap.Count(), nfs.GetExportOptions().MaxHandles)
	}
	nfs.UpdateTuningOptions(func(t *TuningOptions) { t.MaxHandles = -1 })
	if nfs.fileMap.limit() != DefaultMaxHandles || nfs.GetExportOptions().MaxHandles != DefaultMaxHandles {
		t.Errorf("MaxHandles -1 gives limit %d, option %d", nfs.fileMap.limit(), nfs.GetExportOptions().MaxHandles)
	}

	dir := t.TempDir()
	withState, err := New(fs, ExportOptions{HandleStateDir: dir, MaxHandles: 7})
	if err != nil {
		t.Fatal(err)
	}
	defer withState.Close()
	opts = withState.GetExportOptions()
	if opts.HandleStateDir != dir || opts.MaxHandles != 7 {
		t.Errorf("GetExportOptions: dir %q, MaxHandles %d", opts.HandleStateDir, opts.MaxHandles)
	}
	if err := withState.UpdateExportOptions(opts); err != nil {
		t.Errorf("passing GetExportOptions back: %v", err)
	}
	opts.HandleStateDir = ""
	if err := withState.UpdateExportOptions(opts); err != nil {
		t.Errorf("an empty HandleStateDir (unchanged): %v", err)
	}
	opts.HandleStateDir = t.TempDir()
	if err := withState.UpdateExportOptions(opts); err == nil {
		t.Error("HandleStateDir changed at runtime")
	}
}

// A compacted log replays rename records after the coarse array; their
// tombstones are older than what a fold put in the slot, and must not
// hide it: a handle to an object removed after the rename stays stale.
func TestSnapshotReplayKeepsFoldedTombstones(t *testing.T) {
	defer func(a, b int) { tombLimit, coarseSlots = a, b }(tombLimit, coarseSlots)
	tombLimit, coarseSlots = 64, 1<<12
	ctx := context.Background()
	l := testLog()
	rename(l, "a", "b")
	g := l.effective(ctx, "a") // a new object at a
	remove(l, "a")
	for i := 0; i < 70; i++ {
		remove(l, fmt.Sprintf("x%d", i))
	}
	if _, exact := l.tombs.get(l.hash("a")); exact {
		t.Fatal("setup: a's tombstone was not folded")
	}
	follows(t, l, "a", g, "")

	l2 := testLog()
	l2.replay(l.snapshotRecords())
	if got, want := l2.lookup("a"), l.lookup("a"); got < want {
		t.Errorf("after replay lookup(a) = %d, below %d before", got, want)
	}
	follows(t, l2, "a", g, "")
	follows(t, l2, "a", 0, "b") // the renamed object still follows
}

// End to end: the handle of an object removed after a rename at its path
// stays stale across a compaction and a restart, with a third object at
// the path.
func TestRestartAfterCompactionKeepsRemovedStale(t *testing.T) {
	defer func(n int64) { journalSlack = n }(journalSlack)
	journalSlack = 256
	defer func(a, b int) { tombLimit, coarseSlots = a, b }(tombLimit, coarseSlots)
	tombLimit, coarseSlots = 64, 1<<12

	fs := newMemFS(t)
	mustMkdir(t, fs, "/d")
	writeFile(t, fs, "/d/a", 1)
	r := &restartEnv{t: t, fs: fs, dir: t.TempDir()}
	os.WriteFile(filepath.Join(r.dir, handleKeyFile), bytes.Repeat([]byte{3}, handleKeySize), 0o600)
	os.WriteFile(filepath.Join(r.dir, handleLogFile), nil, 0o600)
	e := r.start(ExportOptions{})
	root := e.mount("/")
	d := e.walk(root, "d")
	if st := e.rename(d, "a", d, "b"); st != NFS_OK {
		t.Fatal(st)
	}
	writeFile(t, fs, "/d/a", 2)
	a2 := e.walk(root, "d/a")
	if st := e.remove(d, "a"); st != NFS_OK {
		t.Fatal(st)
	}
	for i := 0; i < 90; i++ {
		name := fmt.Sprintf("f%d", i)
		writeFile(t, fs, "/d/"+name, 1)
		if st := e.remove(d, name); st != NFS_OK {
			t.Fatal(st)
		}
	}
	l := e.nfs.fileMap.log
	if l.folds.Load() == 0 {
		t.Fatal("setup: no fold")
	}
	// Compact now, so the log on disk is a snapshot taken after the fold.
	l.mu.Lock()
	l.journal.mu.Lock()
	l.journal.compact()
	l.journal.mu.Unlock()
	l.mu.Unlock()
	e.stale(a2, "removed object before the restart")
	e.nfs.Close()
	writeFile(t, fs, "/d/a", 3)
	e = r.start(ExportOptions{})
	defer e.nfs.Close()
	e.stale(a2, "removed object after the restart, another at its path")
}

// The consumer case HandleGeneration exists for: a server that rebuilds
// its namespace at start, so /w1 names other content after a restart. With
// the same generation a handle means what it meant (here, the new content
// at its path, which is why such a server must change the generation);
// with a new generation every older handle is stale by its tag alone, with
// no filesystem call, and the state starts afresh, so the old generation's
// handles stay stale if it comes back.
func TestHandleGenerationAcrossRestarts(t *testing.T) {
	cfs := &ioCountingFS{SymlinkFileSystem: newMemFS(t)}
	mustMkdir(t, cfs, "/w1")
	writeFile(t, cfs, "/w1/x", 10)
	// A name too long for an inline handle: its handle is a tail handle,
	// which resolves by a directory walk before its tag is checked.
	long := "w1/" + strings.Repeat("Q7#zK9!vX2@pW4$", 6)
	writeFile(t, cfs, "/"+long, 30)
	r := &restartEnv{t: t, fs: cfs, dir: filepath.Join(t.TempDir(), "state")}
	keyPath, genPath := filepath.Join(r.dir, handleKeyFile), filepath.Join(r.dir, handleGenFile)

	e := r.start(ExportOptions{HandleGeneration: "A"})
	root := e.mount("/")
	x := e.walk(root, "w1/x")
	tail := e.walk(root, long)
	if ph, ok := parseHandle(tail); !ok || ph.kind != fhKindTail {
		t.Fatalf("the handle of %s is not a tail handle", long)
	}
	e.nfs.Close()
	if g, err := os.ReadFile(genPath); err != nil || string(g) != "A" {
		t.Fatalf("handle.gen %q, %v; want \"A\"", g, err)
	}
	keyA, _ := os.ReadFile(keyPath)

	e = r.start(ExportOptions{HandleGeneration: "A"})
	e.size(x, 10, "a handle after a restart with the same generation")
	e.size(tail, 30, "a tail handle after a restart with the same generation")
	if again := e.mount("/"); !bytes.Equal(again, root) {
		t.Error("the root's handle changed across a restart with the same generation")
	}
	if got := e.nfs.GetExportOptions().HandleGeneration; got != "A" {
		t.Errorf("GetExportOptions().HandleGeneration %q, want \"A\"", got)
	}
	e.nfs.Close()

	// The namespace is rebuilt while the server is down: /w1/x is another
	// object now.
	writeFile(t, cfs, "/w1/x", 20)
	writeFile(t, cfs, "/"+long, 40)
	e = r.start(ExportOptions{HandleGeneration: "B"})
	before := cfs.calls.Load()
	e.stale(x, "a handle from generation A under B")
	e.stale(root, "the root's handle from generation A under B")
	if n := cfs.calls.Load() - before; n != 0 {
		t.Errorf("handles from another generation cost %d filesystem calls", n)
	}
	// A tail handle costs at most its capped walk (every listing is an
	// Open, with a few stats) before its tag fails.
	before = cfs.calls.Load()
	e.stale(tail, "a tail handle from generation A under B")
	if n, limit := cfs.calls.Load()-before, int64(3*tailMaxListings); n > limit {
		t.Errorf("a tail handle from another generation cost %d filesystem calls, want at most %d", n, limit)
	}
	e.size(e.walk(e.mount("/"), long), 40, "a tail handle issued under B")
	if again := e.mount("/"); bytes.Equal(again, root) {
		t.Error("the root's handle is the same under a new generation")
	}
	e.size(e.walk(e.mount("/"), "w1/x"), 20, "a handle issued under B")
	e.nfs.Close()
	if keyB, _ := os.ReadFile(keyPath); bytes.Equal(keyB, keyA) {
		t.Error("a new generation kept the old key")
	}

	e = r.start(ExportOptions{HandleGeneration: "A"})
	e.stale(x, "a handle from generation A after B, then A again")
	e.stale(tail, "a tail handle from generation A after B, then A again")
	e.nfs.Close()

	// The empty generation leaves no handle.gen, as before generations.
	e = r.start(ExportOptions{})
	e.stale(x, "a handle from generation A under the empty generation")
	e.nfs.Close()
	if _, err := os.Stat(genPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("handle.gen after a start with the empty generation: %v", err)
	}
}

// A state directory made before HandleGeneration existed (handle.key and
// handle.log, no handle.gen) loads under the empty generation, and the
// handles it issued stay valid: the server issues the same bytes.
func TestHandleStateDirFromBeforeGenerations(t *testing.T) {
	fs := newMemFS(t)
	mustMkdir(t, fs, "/repo/.git")
	writeFile(t, fs, "/repo/.git/index", 10)
	r := &restartEnv{t: t, fs: fs, dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(r.dir, handleKeyFile), bytes.Repeat([]byte{7}, handleKeySize), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, handleLogFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Issued for /repo/.git/index with this key by the server before
	// HandleGeneration (TestHandleCodecEmptyGenerationGolden).
	old, _ := hex.DecodeString("4414c962a4df5989f252b208686e2759176e00")

	e := r.start(ExportOptions{})
	e.size(old, 10, "a handle persisted before generations")
	if fh := e.walk(e.mount("/"), "repo/.git/index"); !bytes.Equal(fh, old) {
		t.Errorf("handle issued now %x, before generations %x", fh, old)
	}
	e.nfs.Close()
	if _, err := os.Stat(filepath.Join(r.dir, handleGenFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("handle.gen written for the empty generation: %v", err)
	}

	e = r.start(ExportOptions{HandleGeneration: "v2"})
	e.stale(old, "a handle persisted before generations, under a generation set later")
	e.nfs.Close()

	// A handle.gen that cannot be read is an error, not a silent reset.
	genPath := filepath.Join(r.dir, handleGenFile)
	os.Remove(genPath)
	if err := os.Mkdir(genPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if nfs, err := New(fs, ExportOptions{HandleStateDir: r.dir, HandleGeneration: "v2"}); err == nil {
		nfs.Close()
		t.Error("an unreadable handle.gen was accepted")
	}
}

// HandleGeneration is fixed at New: UpdateExportOptions rejects a
// different non-empty value and applies nothing, and takes an empty one,
// or the current one, as unchanged.
func TestHandleGenerationFixedAtNew(t *testing.T) {
	fs := newMemFS(t)
	writeFile(t, fs, "/f", 1)
	e := newFhEnv(t, fs, ExportOptions{HandleStateDir: t.TempDir(), HandleGeneration: "g1"})
	f := e.walk(e.mount("/"), "f")
	opts := e.nfs.GetExportOptions()
	if opts.HandleGeneration != "g1" {
		t.Fatalf("GetExportOptions().HandleGeneration %q, want \"g1\"", opts.HandleGeneration)
	}
	if err := e.nfs.UpdateExportOptions(opts); err != nil {
		t.Errorf("passing GetExportOptions back: %v", err)
	}
	opts.HandleGeneration = ""
	if err := e.nfs.UpdateExportOptions(opts); err != nil {
		t.Errorf("an empty HandleGeneration (unchanged): %v", err)
	}
	opts.HandleGeneration, opts.ReadOnly = "g2", true
	if err := e.nfs.UpdateExportOptions(opts); err == nil {
		t.Error("HandleGeneration changed at runtime")
	}
	if got := e.nfs.GetExportOptions(); got.HandleGeneration != "g1" || got.ReadOnly {
		t.Errorf("after a rejected update: generation %q, ReadOnly %v", got.HandleGeneration, got.ReadOnly)
	}
	e.size(f, 1, "a handle after a rejected generation change")
}

// Failing to record a new generation is an error, and leaves no key: the
// next start makes the state afresh again.
func TestHandleGenerationWriteFailure(t *testing.T) {
	fs := newMemFS(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, handleGenFile+".tmp", "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if nfs, err := New(fs, ExportOptions{HandleStateDir: dir, HandleGeneration: "g"}); err == nil {
		nfs.Close()
		t.Fatal("New succeeded without recording its generation")
	}
	if _, err := os.Stat(filepath.Join(dir, handleKeyFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a key was written without its generation: %v", err)
	}
}
