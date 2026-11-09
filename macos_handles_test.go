//go:build darwin

package absnfs

// macOS end-to-end tests of file handles: git workspaces served with a
// handle cache far smaller than the handles the client holds. Before path
// handles, evicted handles gave "Stale NFS file handle" in git status, and
// in c4d, with four workspaces under a switchfs, a warm git status in an
// older workspace took 6-7 s against 0.4-0.7 s in the newest.
//
// Like TestMacOSUserMount they are skipped unless ABSNFS_MACOS_MOUNT=1:
//
//	ABSNFS_MACOS_MOUNT=1 go test -run 'TestMacOSHandles|TestMacOSWorkspaces' -v

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/absfs/absfs"
	"github.com/absfs/unionfs"
)

func skipUnlessMacOSMount(t *testing.T) {
	t.Helper()
	if os.Getenv(macOSMountEnv) != "1" {
		t.Skipf("set %s=1 on macOS to run the mount test", macOSMountEnv)
	}
	if os.Geteuid() == 0 {
		t.Skip("the mount test must run as a normal user, not root")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func TestMacOSHandlesSurviveEviction(t *testing.T) {
	skipUnlessMacOSMount(t)
	for name, mk := range map[string]func(*testing.T) absfs.SymlinkFileSystem{"memfs": newMemFS, "unionfs": newUnionFS} {
		t.Run(name, func(t *testing.T) { runMacOSHandleEviction(t, mk(t)) })
	}
}

// macOSRepoDirs and macOSRepoFiles size the tree makeMacOSRepo makes.
const macOSRepoDirs, macOSRepoFiles = 40, 50

// makeMacOSRepo makes the source tree of a repository below dir.
func makeMacOSRepo(t *testing.T, fs absfs.SymlinkFileSystem, dir string) {
	t.Helper()
	for d := 0; d < macOSRepoDirs; d++ {
		sub := fmt.Sprintf("%s/pkg%02d/internal/generated", dir, d)
		mustMkdir(t, fs, sub)
		for i := 0; i < macOSRepoFiles; i++ {
			writeFile(t, fs, fmt.Sprintf("%s/zz_generated_file_with_a_long_name_%03d.go", sub, i), 10+i)
		}
	}
}

func runMacOSHandleEviction(t *testing.T, fs absfs.SymlinkFileSystem) {
	const maxHandles = 64
	makeMacOSRepo(t, fs, "/repo")
	nfs, mnt := mountMacOSHandles(t, fs, maxHandles, "")
	git := newMacOSGit(t, filepath.Join(mnt, "repo"))
	git.run("git", "init", "-q")
	git.run("git", "add", "-A")
	git.run("git", "commit", "-q", "-m", "tree")
	for i := 0; i < 3; i++ {
		st, took := git.run("git", "status", "--porcelain")
		if st != "" {
			t.Errorf("git status after commit = %q, want clean", st)
		}
		t.Logf("git status %d: %v", i, took)
	}
	out, took := git.run("/usr/bin/find", ".", "-type", "f", "-name", "*.go")
	if n := len(strings.Fields(out)); n != macOSRepoDirs*macOSRepoFiles {
		t.Errorf("find listed %d files, want %d", n, macOSRepoDirs*macOSRepoFiles)
	}
	t.Logf("find: %v", took)

	m := nfs.GetMetrics()
	t.Logf("handle cache: %d of %d, %d misses", m.HandleCacheSize, m.HandleCacheCapacity, m.HandleCacheMisses)
	if m.HandleCacheMisses == 0 || m.HandleCacheSize > maxHandles {
		t.Errorf("the cache did not evict: %d cached, %d misses", m.HandleCacheSize, m.HandleCacheMisses)
	}
}

// TestMacOSWorkspacesWarmStatus is the c4d case: four workspaces under a
// switchfs, each a unionfs over one shared lower layer, with a handle cache
// that holds about one workspace's handles. A warm git status in the
// oldest workspace, whose handles were all evicted, must take about as
// long as one in the newest, whose handles are cached: an evicted handle
// costs a decode and a stat, not an NFS3ERR_STALE and a fresh lookup. It
// mounts with noac, so every lstat git makes reaches the server.
func TestMacOSWorkspacesWarmStatus(t *testing.T) {
	skipUnlessMacOSMount(t)
	const workspaces, maxHandles = 4, 3000 // about 2,300 handles a workspace
	lower := newMemFS(t)
	makeMacOSRepo(t, lower, "/repo")
	routes := map[string]absfs.FileSystem{}
	for w := 0; w < workspaces; w++ {
		upper := newMemFS(t)
		routes[fmt.Sprintf("/ws%d", w)] = unionfs.New(unionfs.WithWritableLayer(upper), unionfs.WithReadOnlyLayer(lower)).SymlinkFileSystem()
	}
	sw := newSwitchFS(t, routes)
	nfs, mnt := mountMacOSHandles(t, sw.fs, maxHandles, ",noac")

	gits := make([]*macOSGit, workspaces)
	for w := range gits {
		gits[w] = newMacOSGit(t, filepath.Join(mnt, fmt.Sprintf("ws%d", w), "repo"))
		gits[w].run("git", "init", "-q")
		gits[w].run("git", "add", "-A")
		gits[w].run("git", "commit", "-q", "-m", "tree")
		gits[w].run("git", "status", "--porcelain")
		m := nfs.GetMetrics()
		t.Logf("after workspace %d: %d handles cached, %d misses", w, m.HandleCacheSize, m.HandleCacheMisses)
	}

	// Warm runs, best of three, newest first: the oldest's handles were
	// evicted by the three workspaces set up after it.
	status := func(g *macOSGit) (time.Duration, uint64) {
		best, misses := time.Duration(1<<62), uint64(0)
		for i := 0; i < 3; i++ {
			before := nfs.GetMetrics().HandleCacheMisses
			st, took := g.run("git", "status", "--porcelain")
			if st != "" {
				t.Errorf("git status = %q, want clean", st)
			}
			if took < best {
				best = took
			}
			misses += nfs.GetMetrics().HandleCacheMisses - before
		}
		return best, misses
	}
	newest, newestMisses := status(gits[workspaces-1])
	oldest, oldestMisses := status(gits[0])
	t.Logf("warm git status: newest %v (%d misses), oldest %v (%d misses)", newest, newestMisses, oldest, oldestMisses)
	if oldestMisses == 0 {
		t.Errorf("the oldest workspace's handles were not evicted: the test does not test eviction")
	}
	// The regression was 10x; allow 3x, plus slack for runs of a few ms.
	if limit := 3*newest + 250*time.Millisecond; oldest > limit {
		t.Errorf("warm git status in the oldest workspace took %v, the newest %v: over %v", oldest, newest, limit)
	}
}

// mountMacOSHandles serves fs with a cache of maxHandles handles, mounts it
// with the kernel client in a fresh directory, and unmounts and stops the
// server when the test ends. extraOpts (",opt,...") is added to the mount options.
func mountMacOSHandles(t *testing.T, fs absfs.SymlinkFileSystem, maxHandles int, extraOpts string) (*AbsfsNFS, string) {
	t.Helper()
	return mountMacOS(t, fs, ExportOptions{MaxHandles: maxHandles}, extraOpts)
}

// mountMacOS serves fs with exportOpts, mounts it with the kernel client in
// a fresh directory, and unmounts and stops the server when the test ends.
// extraOpts (",opt,...") is added to the mount options.
func mountMacOS(t *testing.T, fs absfs.SymlinkFileSystem, exportOpts ExportOptions, extraOpts string) (*AbsfsNFS, string) {
	t.Helper()
	scratch, err := os.MkdirTemp("", "absnfs-handles-")
	if err != nil {
		t.Fatal(err)
	}
	if scratch, err = filepath.EvalSymlinks(scratch); err != nil {
		t.Fatal(err)
	}
	mnt := filepath.Join(scratch, "mnt")
	if err := os.Mkdir(mnt, 0o700); err != nil {
		t.Fatal(err)
	}
	nfs, err := New(fs, exportOpts)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(ServerOptions{Hostname: "127.0.0.1", UseRecordMarking: true})
	if err != nil {
		t.Fatal(err)
	}
	srv.SetHandler(nfs)
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	mounted := false
	// Cleanups run last first: this one after the unmount below. The
	// server stops only once the mount is gone (see serveUntilUnmounted).
	t.Cleanup(func() {
		if mounted {
			serveUntilUnmounted(t, mnt)
		}
		srv.Stop()
		nfs.Close()
		os.RemoveAll(scratch)
	})

	port := srv.GetPort()
	opts := fmt.Sprintf("vers=3,tcp,port=%d,mountport=%d,noresvport,soft,intr,timeo=10,retrans=2,locallocks,noatime%s", port, port, extraOpts)
	if out, err := runWithTimeout(30*time.Second, "", nil, "/sbin/mount_nfs", "-o", opts, "127.0.0.1:/", mnt); err != nil {
		if isMountedAt(mnt) {
			mounted = unmountMacOS(mnt) != nil
		}
		t.Fatalf("mount_nfs: %v\n%s", err, out)
	}
	mounted = true
	t.Cleanup(func() {
		if err := unmountMacOS(mnt); err != nil {
			t.Errorf("unmount %s: %v", mnt, err)
			return
		}
		mounted = false
	})
	return nfs, mnt
}

// macOSGit runs commands in a repository on the mount.
type macOSGit struct {
	t    *testing.T
	repo string
	env  []string
}

func newMacOSGit(t *testing.T, repo string) *macOSGit {
	return &macOSGit{t: t, repo: repo, env: append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=absnfs", "GIT_AUTHOR_EMAIL=absnfs@example.invalid",
		"GIT_COMMITTER_NAME=absnfs", "GIT_COMMITTER_EMAIL=absnfs@example.invalid",
	)}
}

// run runs a command in the repository and returns its output and how long
// it took; output mentioning a stale handle fails the test.
func (g *macOSGit) run(name string, args ...string) (string, time.Duration) {
	g.t.Helper()
	start := time.Now()
	out, err := runWithTimeout(5*time.Minute, g.repo, g.env, name, args...)
	took := time.Since(start)
	if bytes.Contains(bytes.ToLower(out), []byte("stale")) {
		g.t.Errorf("%s %v: %s", name, args, out)
	}
	if err != nil {
		g.t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out), took
}
