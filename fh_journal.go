// fh_journal.go: ExportOptions.HandleStateDir, which keeps file handles
// valid across a server restart.
//
// The directory holds handle.key, the 32-byte key handles are tagged with,
// and handle.log, the change log's records (see fh_changelog.go): one
// record per REMOVE, RMDIR and RENAME, written before the reply, so a
// process crash loses nothing. The log is synced every second and on Close;
// an operating system crash can lose the last second of records. A restart
// replays the log, so a handle issued before it means what it meant: one to
// a removed or replaced object stays stale, one to a renamed object follows
// it. The key alone is never trusted without the log: if either file is
// missing, both are made afresh and every older handle is stale.
//
// handle.gen holds ExportOptions.HandleGeneration when it is not empty
// (absent means the empty generation, so a directory made before
// generations existed loads as it did). A start with a different generation
// makes the key and the log afresh: the old handles fail their tag under
// the new generation anyway, and a new key keeps them stale if the old
// generation ever comes back, by which time its log is gone.
//
// handle.lock is held locked while a server uses the directory, so a second
// server cannot share it.
package absnfs

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	handleKeyFile  = "handle.key"
	handleLogFile  = "handle.log"
	handleGenFile  = "handle.gen"
	handleLockFile = "handle.lock"
	handleKeySize  = 32

	recTomb   = 'T' // path hash, epoch
	recRename = 'R' // epoch, from, src, dst
	recCoarse = 'C' // coarse array slots

	journalSyncInterval = time.Second
)

// journalSlack is how far the log may outgrow twice its live records
// before it is compacted; a variable for tests.
var journalSlack int64 = 1 << 20

var journalCRC = crc32.MakeTable(crc32.Castagnoli)

// handleJournal appends change-log records to handle.log.
type handleJournal struct {
	dir    string
	lock   io.Closer
	onFail func(error)

	mu       sync.Mutex
	f        *os.File
	size     int64 // bytes in handle.log
	base     int64 // size after the last compaction or load
	dirty    bool  // written since the last sync
	failed   bool  // a write failed: the journal is abandoned
	closed   bool
	snapshot func() []byte // the live records, called with the log locked

	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// newFileHandleMapWithState returns a handle cache whose key and change log
// are kept in dir, with handles of generation gen: it locks dir, loads or
// makes the key and the log, and replays the log. onFail is called if a
// later write fails.
func newFileHandleMapWithState(maxHandles int, dir, gen string, onFail func(error)) (*FileHandleMap, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("handle state: %w", err)
	}
	lock, err := lockStateDir(filepath.Join(dir, handleLockFile))
	if err != nil {
		return nil, fmt.Errorf("handle state %s: %w", dir, err)
	}
	fm, f, err := loadHandleState(maxHandles, dir, gen)
	if err != nil {
		lock.Close()
		return nil, fmt.Errorf("handle state %s: %w", dir, err)
	}
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		f.Close()
		lock.Close()
		return nil, fmt.Errorf("handle state %s: %w", dir, err)
	}
	j := &handleJournal{
		dir: dir, lock: lock, onFail: onFail, f: f,
		size: size, base: size,
		snapshot: fm.log.snapshotRecords,
		stop:     make(chan struct{}), done: make(chan struct{}),
	}
	fm.log.journal = j
	go j.syncLoop()
	return fm, nil
}

// loadHandleState returns a handle cache with the key in dir and the log's
// records replayed, and the log opened for appending. Both files are made
// new unless both exist and were made for generation gen.
func loadHandleState(maxHandles int, dir, gen string) (*FileHandleMap, *os.File, error) {
	keyPath, logPath := filepath.Join(dir, handleKeyFile), filepath.Join(dir, handleLogFile)
	genPath := filepath.Join(dir, handleGenFile)
	key, keyErr := os.ReadFile(keyPath)
	logData, logErr := os.ReadFile(logPath)
	stored, genErr := os.ReadFile(genPath)
	if errors.Is(genErr, os.ErrNotExist) {
		stored, genErr = nil, nil // the empty generation
	}
	switch {
	case keyErr == nil && logErr == nil && genErr == nil && string(stored) == gen:
		if len(key) != handleKeySize {
			return nil, nil, fmt.Errorf("%s is %d bytes, want %d", handleKeyFile, len(key), handleKeySize)
		}
		fm := newFileHandleMapWithKey(maxHandles, key, gen)
		// Drop a record torn by a crash, so appends follow good ones. (A
		// file open for appending cannot be truncated on Windows.)
		if good := fm.log.replay(logData); good < len(logData) {
			if err := os.Truncate(logPath, int64(good)); err != nil {
				return nil, nil, err
			}
		}
		f, err := os.OpenFile(logPath, os.O_RDWR|os.O_APPEND, 0o600)
		if err != nil {
			return nil, nil, err
		}
		return fm, f, nil
	case keyErr != nil && !errors.Is(keyErr, os.ErrNotExist):
		return nil, nil, keyErr
	case logErr != nil && !errors.Is(logErr, os.ErrNotExist):
		return nil, nil, logErr
	case genErr != nil:
		return nil, nil, genErr
	}
	// Start afresh. The old key is removed first and the new one written
	// last: a crash in between leaves no key, which starts afresh again.
	if err := os.Remove(keyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	f, err := os.OpenFile(logPath, os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, err
	}
	if gen == "" {
		err = os.Remove(genPath)
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
	} else {
		err = writeFileSync(genPath, []byte(gen))
	}
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	key = make([]byte, handleKeySize)
	if _, err := rand.Read(key); err != nil {
		f.Close()
		return nil, nil, err
	}
	if err := writeFileSync(keyPath, key); err != nil {
		f.Close()
		return nil, nil, err
	}
	return newFileHandleMapWithKey(maxHandles, key, gen), f, nil
}

func writeFileSync(name string, data []byte) error {
	tmp := name + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, name)
}

// appendRecord frames a record: uvarint payload length, payload, CRC-32C.
func appendRecord(dst, payload []byte) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(payload)))
	dst = append(dst, payload...)
	return binary.BigEndian.AppendUint32(dst, crc32.Checksum(payload, journalCRC))
}

func tombPayload(h, e uint64) []byte {
	p := []byte{recTomb}
	p = binary.BigEndian.AppendUint64(p, h)
	return binary.BigEndian.AppendUint64(p, e)
}

func renamePayload(e, from uint64, src, dst string) []byte {
	p := []byte{recRename}
	p = binary.BigEndian.AppendUint64(p, e)
	p = binary.BigEndian.AppendUint64(p, from)
	p = binary.AppendUvarint(p, uint64(len(src)))
	p = append(p, src...)
	p = binary.AppendUvarint(p, uint64(len(dst)))
	return append(p, dst...)
}

// recordTomb journals a REMOVE or RMDIR. A nil journal records nothing.
func (j *handleJournal) recordTomb(h, e uint64) {
	if j != nil {
		j.write(appendRecord(nil, tombPayload(h, e)))
	}
}

// recordRename journals a RENAME. A nil journal records nothing.
func (j *handleJournal) recordRename(e, from uint64, src, dst string) {
	if j != nil {
		j.write(appendRecord(nil, renamePayload(e, from, src, dst)))
	}
}

// write appends a framed record, and compacts the log once it holds more
// than twice its live records plus journalSlack. It runs with the change
// log's lock held, so snapshot sees the state the records led to.
func (j *handleJournal) write(rec []byte) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.failed || j.closed {
		return
	}
	if _, err := j.f.Write(rec); err != nil {
		j.fail(err)
		return
	}
	j.size += int64(len(rec))
	j.dirty = true
	if j.size > 2*j.base+journalSlack {
		j.compact()
	}
}

// compact replaces the log with a snapshot of the live records. The caller
// holds j.mu.
func (j *handleJournal) compact() {
	snap := j.snapshot()
	logPath := filepath.Join(j.dir, handleLogFile)
	tmp := logPath + ".new"
	if err := os.WriteFile(tmp, snap, 0o600); err != nil {
		j.fail(err)
		return
	}
	if err := syncFile(tmp); err != nil {
		j.fail(err)
		return
	}
	// Closed first: Windows cannot replace a file that is open.
	j.f.Close()
	renameErr := os.Rename(tmp, logPath)
	f, err := os.OpenFile(logPath, os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		j.fail(err)
		return
	}
	j.f = f
	if renameErr != nil {
		j.fail(renameErr)
		return
	}
	j.size, j.base, j.dirty = int64(len(snap)), int64(len(snap)), false
}

func syncFile(name string) error {
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// fail abandons the journal after a write failed, and removes the key so
// the next start makes a new one: handles from before then are stale,
// which is safe, where trusting them with records missing is not. The
// caller holds j.mu.
func (j *handleJournal) fail(err error) {
	j.failed = true
	os.Remove(filepath.Join(j.dir, handleKeyFile))
	if j.onFail != nil {
		j.onFail(err)
	}
}

func (j *handleJournal) syncLoop() {
	defer close(j.done)
	t := time.NewTicker(journalSyncInterval)
	defer t.Stop()
	for {
		select {
		case <-j.stop:
			return
		case <-t.C:
			j.sync()
		}
	}
}

func (j *handleJournal) sync() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.dirty || j.failed || j.closed {
		return
	}
	if err := j.f.Sync(); err != nil {
		j.fail(err)
		return
	}
	j.dirty = false
}

// close syncs and closes the log and unlocks the directory. Later calls
// do nothing.
func (j *handleJournal) close() error {
	if j == nil {
		return nil
	}
	var err error
	j.closeOnce.Do(func() {
		close(j.stop)
		<-j.done
		j.sync()
		j.mu.Lock()
		j.closed = true
		err = j.f.Close()
		j.mu.Unlock()
		if lerr := j.lock.Close(); err == nil {
			err = lerr
		}
	})
	return err
}

// replay applies the records of a log to l, which is empty, and returns
// the length of the good records: a torn or corrupt record ends the log.
func (l *changeLog) replay(data []byte) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := bytes.NewReader(data)
	good := 0
	for r.Len() > 0 {
		n, err := binary.ReadUvarint(r)
		if err != nil || n > uint64(r.Len()) || n == 0 {
			break
		}
		payload := make([]byte, n)
		io.ReadFull(r, payload)
		var sum [4]byte
		if _, err := io.ReadFull(r, sum[:]); err != nil || binary.BigEndian.Uint32(sum[:]) != crc32.Checksum(payload, journalCRC) {
			break
		}
		if !l.apply(payload) {
			break
		}
		good = len(data) - r.Len()
	}
	return good
}

// apply replays one record. The caller holds l.mu.
func (l *changeLog) apply(p []byte) bool {
	r := bufio.NewReader(bytes.NewReader(p[1:]))
	u64 := func() (uint64, bool) {
		var b [8]byte
		_, err := io.ReadFull(r, b[:])
		return binary.BigEndian.Uint64(b[:]), err == nil
	}
	str := func() (string, bool) {
		n, err := binary.ReadUvarint(r)
		if err != nil || n > uint64(len(p)) {
			return "", false
		}
		b := make([]byte, n)
		_, err = io.ReadFull(r, b)
		return string(b), err == nil
	}
	switch p[0] {
	case recTomb:
		h, ok1 := u64()
		e, ok2 := u64()
		if !ok1 || !ok2 {
			return false
		}
		l.setTombHash(h, e)
		l.last = max(l.last, e)
	case recRename:
		e, ok1 := u64()
		from, ok2 := u64()
		src, ok3 := str()
		dst, ok4 := str()
		if !ok1 || !ok2 || !ok3 || !ok4 {
			return false
		}
		l.setTomb(src, e)
		l.setTomb(dst, e)
		l.addRedirect(src, redirect{e: e, from: from, dst: dst})
		l.last = max(l.last, e)
	case recCoarse:
		if l.coarse == nil {
			l.coarse = make([]uint64, coarseSlots)
		}
		for {
			slot, err := binary.ReadUvarint(r)
			if err == io.EOF {
				break
			}
			e, ok := u64()
			if err != nil || !ok || slot >= uint64(len(l.coarse)) {
				return false
			}
			l.coarse[slot] = max(l.coarse[slot], e)
			l.last = max(l.last, e)
		}
	default:
		return false
	}
	l.active.Store(true)
	return true
}

// snapshotRecords returns records that rebuild l's state: the coarse
// array, the exact tombstones, and the rename records, least recently
// recorded first so the LRU comes back in order. A rename record also sets
// tombstones, which are already in the snapshot or older. The caller holds
// l.mu (it runs from a journal write during a commit).
func (l *changeLog) snapshotRecords() []byte {
	var out []byte
	if l.coarse != nil {
		const perRecord = 4096
		var p []byte
		for slot, e := range l.coarse {
			if e == 0 {
				continue
			}
			if p == nil {
				p = []byte{recCoarse}
			}
			p = binary.AppendUvarint(p, uint64(slot))
			p = binary.BigEndian.AppendUint64(p, e)
			if len(p) > perRecord*12 {
				out = appendRecord(out, p)
				p = nil
			}
		}
		if p != nil {
			out = appendRecord(out, p)
		}
	}
	for _, t := range l.tombs.entries() {
		out = appendRecord(out, tombPayload(t.h, t.e))
	}
	for el := l.lru.Back(); el != nil; el = el.Prev() {
		rl := el.Value.(*redirectList)
		for _, r := range rl.recs {
			out = appendRecord(out, renamePayload(r.e, r.from, rl.src, r.dst))
		}
	}
	return out
}
