//go:build darwin

package absnfs

// macOS end-to-end test of file handles: a git workspace served with a
// handle cache far smaller than the handles the client holds. Before path
// handles, evicted handles gave "Stale NFS file handle" in git status.
//
// Like TestMacOSUserMount it is skipped unless ABSNFS_MACOS_MOUNT=1:
//
//	ABSNFS_MACOS_MOUNT=1 go test -run TestMacOSHandlesSurviveEviction -v

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
)

func TestMacOSHandlesSurviveEviction(t *testing.T) {
	if os.Getenv(macOSMountEnv) != "1" {
		t.Skipf("set %s=1 on macOS to run the mount test", macOSMountEnv)
	}
	if os.Geteuid() == 0 {
		t.Skip("the mount test must run as a normal user, not root")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for name, mk := range map[string]func(*testing.T) absfs.SymlinkFileSystem{"memfs": newMemFS, "unionfs": newUnionFS} {
		t.Run(name, func(t *testing.T) { runMacOSHandleEviction(t, mk(t)) })
	}
}

func runMacOSHandleEviction(t *testing.T, fs absfs.SymlinkFileSystem) {
	const dirs, files, maxHandles = 40, 50, 64
	for d := 0; d < dirs; d++ {
		dir := fmt.Sprintf("/repo/pkg%02d/internal/generated", d)
		mustMkdir(t, fs, dir)
		for i := 0; i < files; i++ {
			writeFile(t, fs, fmt.Sprintf("%s/zz_generated_file_with_a_long_name_%03d.go", dir, i), 10+i)
		}
	}

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
	nfs, err := New(fs, ExportOptions{MaxHandles: maxHandles})
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
	defer func() {
		srv.Stop()
		nfs.Close()
		if mounted {
			t.Errorf("left %s mounted; not removing %s", mnt, scratch)
			return
		}
		os.RemoveAll(scratch)
	}()

	port := srv.GetPort()
	opts := fmt.Sprintf("vers=3,tcp,port=%d,mountport=%d,noresvport,soft,intr,timeo=10,retrans=2,locallocks,noatime", port, port)
	if out, err := runWithTimeout(30*time.Second, "", nil, "/sbin/mount_nfs", "-o", opts, "127.0.0.1:/", mnt); err != nil {
		if isMountedAt(mnt) {
			mounted = unmountMacOS(mnt) != nil
		}
		t.Fatalf("mount_nfs: %v\n%s", err, out)
	}
	mounted = true
	defer func() {
		if err := unmountMacOS(mnt); err != nil {
			t.Errorf("unmount %s: %v", mnt, err)
			return
		}
		mounted = false
	}()

	repo := filepath.Join(mnt, "repo")
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=absnfs", "GIT_AUTHOR_EMAIL=absnfs@example.invalid",
		"GIT_COMMITTER_NAME=absnfs", "GIT_COMMITTER_EMAIL=absnfs@example.invalid",
	)
	run := func(name string, args ...string) (string, time.Duration) {
		t.Helper()
		start := time.Now()
		out, err := runWithTimeout(5*time.Minute, repo, env, name, args...)
		took := time.Since(start)
		if bytes.Contains(bytes.ToLower(out), []byte("stale")) {
			t.Errorf("%s %v: %s", name, args, out)
		}
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out), took
	}
	run("git", "init", "-q")
	run("git", "add", "-A")
	run("git", "commit", "-q", "-m", "tree")
	for i := 0; i < 3; i++ {
		st, took := run("git", "status", "--porcelain")
		if st != "" {
			t.Errorf("git status after commit = %q, want clean", st)
		}
		t.Logf("git status %d: %v", i, took)
	}
	out, took := run("/usr/bin/find", ".", "-type", "f", "-name", "*.go")
	if n := len(strings.Fields(out)); n != dirs*files {
		t.Errorf("find listed %d files, want %d", n, dirs*files)
	}
	t.Logf("find: %v", took)

	m := nfs.GetMetrics()
	t.Logf("handle cache: %d of %d, %d misses", m.HandleCacheSize, m.HandleCacheCapacity, m.HandleCacheMisses)
	if m.HandleCacheMisses == 0 || m.HandleCacheSize > maxHandles {
		t.Errorf("the cache did not evict: %d cached, %d misses", m.HandleCacheSize, m.HandleCacheMisses)
	}
}
