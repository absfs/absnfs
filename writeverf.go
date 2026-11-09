// writeverf.go: the write verifier WRITE and COMMIT replies carry.
package absnfs

import "crypto/rand"

// newWriteVerifier returns a random verifier, neither zero nor old.
func newWriteVerifier(old [8]byte) [8]byte {
	for {
		var v [8]byte
		rand.Read(v[:])
		if v != ([8]byte{}) && v != old {
			return v
		}
	}
}

// writeVerifier returns the verifier replies carry now.
//
// A WRITE reads it before writing, and a COMMIT after syncing. An owner
// that discards uncommitted data changes the verifier first and then
// discards only what was written before the change (see
// ExportOptions.WriteVerifier). A WRITE whose data may be discarded then
// read the old verifier, and a COMMIT that replies the old one synced
// before the change, so nothing it covers could be discarded.
func (s *AbsfsNFS) writeVerifier() [8]byte {
	return s.tuning.Load().WriteVerifier
}

// unstableDataLost changes the write verifier after a Sync or Close that
// failed on a file written since its last Sync, as Linux knfsd does on a
// writeback error: data replied UNSTABLE may be gone, and clients send
// again what they have not seen committed.
func (s *AbsfsNFS) unstableDataLost(p string, err error) {
	s.UpdateTuningOptions(func(t *TuningOptions) {
		t.WriteVerifier = newWriteVerifier(t.WriteVerifier)
	})
	if slog := s.getStructuredLogger(); slog != nil {
		slog.Warn("unstable writes may be lost, write verifier changed",
			LogField{Key: "path", Value: p},
			LogField{Key: "error", Value: err})
	}
}
