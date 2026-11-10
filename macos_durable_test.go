//go:build darwin

package absnfs

// macOS end-to-end measurements for absfs/absnfs#91 and #92, run like the
// other mount tests (see macos_mount_test.go for the safety rules):
//
//	ABSNFS_MACOS_MOUNT=1 go test -run 'TestMacOSDurability|TestMacOSUndescribedEntry' -v -timeout 0
//
// The client mounts a tap in front of the server that counts the WRITE
// calls by their stable_how and the COMMIT calls on the wire, and the
// backend counts its File.Sync calls, opens and closes.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/absfs/absfs"
)

// rpcTap forwards TCP connections to an NFS server, counting the WRITE
// calls by stable_how and the COMMIT calls the client sends.
type rpcTap struct {
	ln     net.Listener
	target string

	mu      sync.Mutex
	writes  [3]int // calls by stable_how: UNSTABLE, DATA_SYNC, FILE_SYNC
	replied [3]int // NFS3_OK replies by committed
	commits int
	pending map[uint32]bool // xids of WRITE calls awaiting their reply

	// With ABSNFS_TAP_TRACE=1, every NFS call and reply is logged.
	trace  func(format string, args ...any)
	callAt map[uint32]time.Time
	procOf map[uint32]uint32
	wg     sync.WaitGroup
	conns  []net.Conn
}

func newRPCTap(t *testing.T, target string) *rpcTap {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tap := &rpcTap{ln: ln, target: target, pending: map[uint32]bool{}, callAt: map[uint32]time.Time{}, procOf: map[uint32]uint32{}}
	if os.Getenv("ABSNFS_TAP_TRACE") == "1" {
		tap.trace = t.Logf
	}
	tap.wg.Add(1)
	go tap.accept()
	return tap
}

func (p *rpcTap) port() int { return p.ln.Addr().(*net.TCPAddr).Port }

func (p *rpcTap) accept() {
	defer p.wg.Done()
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		s, err := net.Dial("tcp", p.target)
		if err != nil {
			c.Close()
			continue
		}
		p.mu.Lock()
		p.conns = append(p.conns, c, s)
		p.mu.Unlock()
		p.wg.Add(2)
		go func() { defer p.wg.Done(); p.forward(s, c, p.countReply); c.Close() }()
		go func() { defer p.wg.Done(); p.forward(c, s, p.count); s.Close() }()
	}
}

// forward copies record-marked RPC messages from c to s, passing each to
// note.
func (p *rpcTap) forward(c, s net.Conn, note func([]byte)) {
	var record []byte
	var hdr [4]byte
	for {
		if _, err := io.ReadFull(c, hdr[:]); err != nil {
			return
		}
		n := binary.BigEndian.Uint32(hdr[:])
		frag := make([]byte, n&0x7fffffff)
		if _, err := io.ReadFull(c, frag); err != nil {
			return
		}
		if _, err := s.Write(append(hdr[:], frag...)); err != nil {
			return
		}
		record = append(record, frag...)
		if n&0x80000000 != 0 {
			note(record)
			record = record[:0]
		}
	}
}

// count notes an NFSv3 WRITE or COMMIT call.
func (p *rpcTap) count(r []byte) {
	u32 := func(off int) (uint32, bool) {
		if off+4 > len(r) {
			return 0, false
		}
		return binary.BigEndian.Uint32(r[off:]), true
	}
	// xid, CALL, rpcvers, prog, vers, proc, then cred and verf.
	if typ, _ := u32(4); typ != 0 {
		return
	}
	prog, _ := u32(12)
	vers, _ := u32(16)
	proc, _ := u32(20)
	if p.trace != nil && prog == 100003 {
		xid, _ := u32(0)
		p.mu.Lock()
		p.callAt[xid], p.procOf[xid] = time.Now(), proc
		p.mu.Unlock()
	}
	if prog != 100003 || vers != 3 || (proc != 7 && proc != 21) {
		return
	}
	off := 24
	for i := 0; i < 2; i++ { // cred, verf: flavor, length, body
		l, ok := u32(off + 4)
		if !ok {
			return
		}
		off += 8 + int(l+3)&^3
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if proc == 21 {
		p.commits++
		return
	}
	xid, _ := u32(0)
	p.pending[xid] = true
	fhLen, ok := u32(off)
	if !ok {
		return
	}
	off += 4 + int(fhLen+3)&^3 + 8 + 4 // handle, offset, count
	if stable, ok := u32(off); ok && stable < 3 {
		p.writes[stable]++
	}
}

// countReply notes the committed level of an NFS3_OK reply to a WRITE.
func (p *rpcTap) countReply(r []byte) {
	u32 := func(off int) (uint32, bool) {
		if off+4 > len(r) {
			return 0, false
		}
		return binary.BigEndian.Uint32(r[off:]), true
	}
	xid, _ := u32(0)
	p.mu.Lock()
	defer p.mu.Unlock()
	if at, ok := p.callAt[xid]; ok && p.trace != nil {
		l, _ := u32(16)
		st, _ := u32(20 + int(l+3)&^3 + 4)
		p.trace("tap: xid %08x proc %d status %d after %v", xid, p.procOf[xid], st, time.Since(at).Round(time.Millisecond))
		delete(p.callAt, xid)
	}
	if !p.pending[xid] {
		return
	}
	delete(p.pending, xid)
	// xid, REPLY, MSG_ACCEPTED, verf (flavor, length, body), SUCCESS, status
	l, ok := u32(16)
	if !ok {
		return
	}
	off := 20 + int(l+3)&^3
	if acc, _ := u32(off); acc != 0 {
		return
	}
	if st, _ := u32(off + 4); st != NFS_OK {
		return
	}
	off += 8
	if pre, _ := u32(off); pre == 1 { // pre_op_attr
		off += 24
	}
	off += 4
	if post, _ := u32(off); post == 1 { // post_op_attr
		off += 84
	}
	off += 4 + 4 // post_op_attr's flag, count
	if committed, ok := u32(off); ok && committed < 3 {
		p.replied[committed]++
	}
}

func (p *rpcTap) take() (writes, replied [3]int, commits int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	writes, replied, commits = p.writes, p.replied, p.commits
	p.writes, p.replied, p.commits = [3]int{}, [3]int{}, 0
	return writes, replied, commits
}

func (p *rpcTap) close() {
	p.ln.Close()
	p.mu.Lock()
	for _, c := range p.conns {
		c.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
}

// mountMacOSTapped serves fs, mounts it through an rpcTap in a fresh
// directory, and unmounts, then stops the tap and the server, when the
// test ends: the server outlives the mount (see serveUntilUnmounted).
func mountMacOSTapped(t *testing.T, fs absfs.SymlinkFileSystem, exportOpts ExportOptions, extraOpts string) (*AbsfsNFS, *rpcTap, string) {
	t.Helper()
	scratch, err := os.MkdirTemp("", "absnfs-durable-")
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
	tap := newRPCTap(t, fmt.Sprintf("127.0.0.1:%d", srv.GetPort()))
	mounted := false
	t.Cleanup(func() {
		if mounted {
			serveUntilUnmounted(t, mnt)
		}
		tap.close()
		srv.Stop()
		nfs.Close()
		os.RemoveAll(scratch)
	})

	port := tap.port()
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
	t.Logf("mounted on %s with -o %s", mnt, opts)
	return nfs, tap, mnt
}

// quiesce waits until the tap has seen no WRITE or COMMIT for a second (at
// most 10), so a count includes what the client sends after close.
func quiesce(tap *rpcTap) {
	last := -1
	for i := 0; i < 10; i++ {
		tap.mu.Lock()
		n := tap.writes[0] + tap.writes[1] + tap.writes[2] + tap.commits
		tap.mu.Unlock()
		if n == last {
			return
		}
		last = n
		time.Sleep(time.Second)
	}
}

func TestMacOSDurability(t *testing.T) {
	if os.Getenv(macOSMountEnv) != "1" {
		t.Skipf("set %s=1 on macOS to run the mount test", macOSMountEnv)
	}
	if os.Geteuid() == 0 {
		t.Skip("the mount test must run as a normal user, not root")
	}
	sfs := &syncFS{SymlinkFileSystem: newMemFS(t)}
	nfs, tap, mnt := mountMacOSTapped(t, sfs, ExportOptions{}, "")

	if tap.trace != nil {
		// Dump the goroutines once while a call is unanswered for 2s.
		done := make(chan struct{})
		defer close(done)
		go func() {
			for {
				select {
				case <-done:
					return
				case <-time.After(500 * time.Millisecond):
				}
				tap.mu.Lock()
				stuck := false
				for _, at := range tap.callAt {
					stuck = stuck || time.Since(at) > 2*time.Second
				}
				tap.mu.Unlock()
				if !stuck {
					continue
				}
				if f, err := os.Create(filepath.Join(os.TempDir(), "absnfs-goroutines-stuck.txt")); err == nil {
					pprof.Lookup("goroutine").WriteTo(f, 2)
					f.Close()
				}
				return
			}
		}()
	}

	const size = 1 << 20
	data := bytes.Repeat([]byte("absnfs#91 "), size/10+1)[:size]
	src := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}

	// The writes run in another process: one blocked in write(2) on the
	// mount can stall this process, and the server in it, until the soft
	// mount gives up (seen with F_FULLFSYNC: the whole process froze for
	// 19 seconds and the client failed the sync with ENXIO).
	writeFile := func(name, sync string) error {
		env := append(os.Environ(), "ABSNFS_WRITER_SRC="+src, "ABSNFS_WRITER_DST="+filepath.Join(mnt, name), "ABSNFS_WRITER_SYNC="+sync)
		out, err := runWithTimeout(60*time.Second, "", env, os.Args[0], "-test.run=^TestMacOSWriterHelper$")
		if err != nil {
			return fmt.Errorf("writer: %v: %s", err, out)
		}
		return nil
	}
	cases := []struct {
		name string
		run  func(name string) error
	}{
		{"write+fsync", func(name string) error { return writeFile(name, "fsync") }},
		{"write+F_FULLFSYNC", func(name string) error { return writeFile(name, "fullfsync") }},
		{"write, no sync", func(name string) error { return writeFile(name, "") }},
		{"cp", func(name string) error {
			out, err := runWithTimeout(60*time.Second, "", nil, "/bin/cp", src, filepath.Join(mnt, name))
			if err != nil {
				return fmt.Errorf("cp: %v: %s", err, out)
			}
			return nil
		}},
	}
	for i, c := range cases {
		name := fmt.Sprintf("f%d", i)
		quiesce(tap)
		tap.take()
		syncs, opens, closes := sfs.syncs.Load(), sfs.opens.Load(), sfs.closes.Load()
		start := time.Now()
		if err := c.run(name); err != nil {
			t.Errorf("%s: %v (after %v)", c.name, err, time.Since(start).Round(time.Millisecond))
		}
		quiesce(tap)
		if tap.trace != nil {
			tap.mu.Lock()
			for xid, at := range tap.callAt {
				t.Logf("tap: xid %08x proc %d unanswered for %v", xid, tap.procOf[xid], time.Since(at).Round(time.Millisecond))
			}
			unanswered := len(tap.callAt)
			tap.mu.Unlock()
			if unanswered > 0 {
				if f, err := os.Create(filepath.Join(os.TempDir(), "absnfs-goroutines.txt")); err == nil {
					pprof.Lookup("goroutine").WriteTo(f, 2)
					f.Close()
					t.Logf("goroutines written to %s", f.Name())
				}
			}
		}
		writes, replied, commits := tap.take()
		t.Logf("%-18s WRITE asked UNSTABLE/DATA_SYNC/FILE_SYNC %d/%d/%d, replied %d/%d/%d; COMMIT %d; backend Sync %d, opens %d, closes %d",
			c.name, writes[0], writes[1], writes[2], replied[0], replied[1], replied[2], commits,
			sfs.syncs.Load()-syncs, sfs.opens.Load()-opens, sfs.closes.Load()-closes)
		if got := readBack(t, sfs, "/"+name); got != string(data) {
			t.Errorf("%s: the backend holds %d bytes, not what was written", c.name, len(got))
		}
		if writes[0]+writes[1]+writes[2] == 0 {
			t.Errorf("%s: no WRITE seen", c.name)
		}
		// Every WRITE is answered FILE_SYNC after a backend Sync (see
		// writeVerf), so the client has nothing to COMMIT.
		total := writes[0] + writes[1] + writes[2]
		if want := [3]int{0, 0, total}; replied != want {
			t.Errorf("%s: replied %v to WRITEs asking %v, want %v", c.name, replied, writes, want)
		}
		if got := sfs.syncs.Load() - syncs; got < int64(total) {
			t.Errorf("%s: %d backend Syncs for %d WRITEs", c.name, got, total)
		}
	}
	t.Logf("files held at the end: %d", nfs.files.count())
}

// TestMacOSWriterHelper is TestMacOSDurability's writer, run in a process
// of its own: it copies ABSNFS_WRITER_SRC to ABSNFS_WRITER_DST with one
// write(2), then syncs it as ABSNFS_WRITER_SYNC says (fsync, fullfsync, or
// not at all), and closes it.
func TestMacOSWriterHelper(t *testing.T) {
	dst := os.Getenv("ABSNFS_WRITER_DST")
	if dst == "" {
		t.Skip("run by TestMacOSDurability")
	}
	data, err := os.ReadFile(os.Getenv("ABSNFS_WRITER_SRC"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	switch os.Getenv("ABSNFS_WRITER_SYNC") {
	case "fsync":
		err = syscall.Fsync(int(f.Fd()))
	case "fullfsync":
		err = f.Sync() // F_FULLFSYNC on darwin
	}
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMacOSUndescribedEntry(t *testing.T) {
	requireFaultInjectionVM(t)
	if os.Getenv(macOSMountEnv) != "1" {
		t.Skipf("set %s=1 on macOS to run the mount test", macOSMountEnv)
	}
	if os.Geteuid() == 0 {
		t.Skip("the mount test must run as a normal user, not root")
	}
	for _, extra := range []string{"", ",rdirplus"} {
		t.Run("opts"+extra, func(t *testing.T) {
			mfs := newMemFS(t)
			mustMkdir(t, mfs, "/d")
			writeFile(t, mfs, "/d/good", 4)
			writeFile(t, mfs, "/d/bad", 5)
			hook := &faHookFS{SymlinkFileSystem: mfs, lstatErr: map[string]error{"/d/bad": syscall.EIO}}
			_, _, mnt := mountMacOSTapped(t, hook, ExportOptions{}, extra)
			d := filepath.Join(mnt, "d")

			run := func(name string, args ...string) error {
				out, err := runWithTimeout(20*time.Second, "", nil, name, args...)
				t.Logf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
				return err
			}
			bad := filepath.Join(d, "bad")
			run("/bin/ls", "-l", d)
			run("/bin/ls", "-1", d)
			run("/bin/ls", "-1f", d)
			run("/usr/bin/find", d)
			stErr := run("/usr/bin/stat", bad)
			catErr := run("/bin/cat", bad)

			// The names the client's READDIR gave, without a stat of each.
			f, err := os.Open(d)
			if err != nil {
				t.Fatal(err)
			}
			names, err := f.Readdirnames(-1)
			f.Close()
			sort.Strings(names)
			t.Logf("getdirentries: %v, %v", names, err)
			if !equalStrings(names, []string{"bad", "good"}) {
				t.Errorf("the client's directory listing is %v, want bad and good", names)
			}
			if stErr == nil || catErr == nil {
				t.Errorf("stat or cat of bad succeeded")
			}
		})
	}
}
