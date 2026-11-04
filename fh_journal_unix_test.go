//go:build unix

package absnfs

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// A start with a new generation that cannot remove the old key is an
// error, and the old key is not used under the new generation.
func TestHandleGenerationKeyRemoveFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	fs := newMemFS(t)
	dir := filepath.Join(t.TempDir(), "state")
	nfs, err := New(fs, ExportOptions{HandleStateDir: dir, HandleGeneration: "A"})
	if err != nil {
		t.Fatal(err)
	}
	nfs.Close()
	key, _ := os.ReadFile(filepath.Join(dir, handleKeyFile))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if nfs, err := New(fs, ExportOptions{HandleStateDir: dir, HandleGeneration: "B"}); err == nil {
		nfs.Close()
		t.Fatal("New succeeded in a state directory it cannot change")
	}
	if again, _ := os.ReadFile(filepath.Join(dir, handleKeyFile)); !bytes.Equal(again, key) {
		t.Error("the failed start changed the key")
	}
	if g, _ := os.ReadFile(filepath.Join(dir, handleGenFile)); string(g) != "A" {
		t.Errorf("handle.gen %q after the failed start, want \"A\"", g)
	}
}
