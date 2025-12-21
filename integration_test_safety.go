//go:build integration

package absnfs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// SafeTestMount provides a safe wrapper for NFS mount operations during integration testing.
// It enforces strict path validation and cleanup to prevent accidental damage to the system.
type SafeTestMount struct {
	SessionID  string
	MountPoint string
	MarkerFile string
	NFSPort    int
	MountPort  int
	IsMounted  bool
	LogFile    string

	mu sync.Mutex
}

// Allowed mount point patterns - ONLY these patterns are permitted
var allowedMountPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^/Volumes/absnfs-[a-zA-Z0-9_-]+$`),
	regexp.MustCompile(`^/tmp/absnfs-[a-zA-Z0-9_-]+$`),
	regexp.MustCompile(`^/private/tmp/absnfs-[a-zA-Z0-9_-]+$`),
}

// Blocked paths - operations on these paths are ALWAYS forbidden
var blockedPaths = []string{
	"/",
	"/Applications",
	"/Library",
	"/System",
	"/Users",
	"/Volumes",
	"/bin",
	"/boot",
	"/dev",
	"/etc",
	"/home",
	"/lib",
	"/lib64",
	"/opt",
	"/private",
	"/proc",
	"/root",
	"/sbin",
	"/sys",
	"/tmp",
	"/usr",
	"/var",
}

// NewSafeTestMount creates a new SafeTestMount with a unique session ID.
func NewSafeTestMount(nfsPort, mountPort int) (*SafeTestMount, error) {
	sessionID := fmt.Sprintf("absnfs-%s-%d", time.Now().Format("20060102-150405"), os.Getpid())
	// Use /Volumes on macOS (root filesystem is read-only, /mnt doesn't exist)
	mountPoint := filepath.Join("/Volumes", sessionID)
	// Marker file in /tmp (no sudo needed)
	markerFile := filepath.Join("/tmp", fmt.Sprintf(".absnfs-marker-%s", sessionID))

	s := &SafeTestMount{
		SessionID:  sessionID,
		MountPoint: mountPoint,
		MarkerFile: markerFile,
		NFSPort:    nfsPort,
		MountPort:  mountPort,
		LogFile:    "/tmp/absnfs-test-operations.log",
	}

	// Validate the generated mount point
	if err := s.validatePath(mountPoint); err != nil {
		return nil, fmt.Errorf("invalid mount point: %w", err)
	}

	return s, nil
}

// validatePath checks if a path is safe for operations.
func (s *SafeTestMount) validatePath(path string) error {
	// Must be absolute
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute: %s", path)
	}

	// Clean the path to prevent traversal
	cleanPath := filepath.Clean(path)

	// Check against blocked paths
	for _, blocked := range blockedPaths {
		if cleanPath == blocked {
			return fmt.Errorf("BLOCKED: path is a protected system path: %s", path)
		}
		// Also check if it's directly under a blocked path (but not our allowed patterns)
		// Exception: allow /Volumes, /mnt, /tmp for test mount points
		if strings.HasPrefix(cleanPath, blocked+"/") && blocked != "/mnt" && blocked != "/tmp" && blocked != "/Volumes" {
			return fmt.Errorf("BLOCKED: path is under protected system path: %s", path)
		}
	}

	// Must match an allowed pattern
	matched := false
	for _, pattern := range allowedMountPatterns {
		if pattern.MatchString(cleanPath) {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("path does not match allowed patterns: %s", path)
	}

	return nil
}

// log writes an entry to the operations log file.
func (s *SafeTestMount) log(operation, details string) {
	entry := fmt.Sprintf("[%s] %s: %s\n", time.Now().Format("2006-01-02 15:04:05"), operation, details)

	f, err := os.OpenFile(s.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: Could not write to log: %v\n", err)
		return
	}
	defer f.Close()
	f.WriteString(entry)
}

// Prepare creates the mount point directory with all safety checks.
func (s *SafeTestMount) Prepare() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log("SESSION_START", s.SessionID)

	// Validate path again
	if err := s.validatePath(s.MountPoint); err != nil {
		return err
	}

	// Check if mount point already exists (it shouldn't)
	if _, err := os.Stat(s.MountPoint); err == nil {
		return fmt.Errorf("SAFETY: mount point already exists: %s (refusing to proceed)", s.MountPoint)
	}

	// Ensure base directory exists (/Volumes should always exist on macOS)
	baseDir := filepath.Dir(s.MountPoint)
	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		return fmt.Errorf("base directory does not exist: %s", baseDir)
	}

	// Create mount point
	s.log("CREATE_MOUNTPOINT", s.MountPoint)
	cmd := exec.Command("sudo", "mkdir", "-p", s.MountPoint)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create mount point: %w: %s", err, output)
	}

	// Create marker file in /tmp (no sudo needed)
	if err := os.WriteFile(s.MarkerFile, []byte(fmt.Sprintf("SESSION=%s\n", s.SessionID)), 0644); err != nil {
		return fmt.Errorf("failed to create marker file: %w", err)
	}

	// Verify mount point is empty
	entries, err := os.ReadDir(s.MountPoint)
	if err != nil {
		return fmt.Errorf("failed to read mount point: %w", err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("SAFETY: mount point is not empty: %s", s.MountPoint)
	}

	return nil
}

// Mount performs the NFS mount with all safety checks.
func (s *SafeTestMount) Mount() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate path
	if err := s.validatePath(s.MountPoint); err != nil {
		return err
	}

	// Verify mount point exists and is empty
	info, err := os.Stat(s.MountPoint)
	if err != nil {
		return fmt.Errorf("mount point does not exist: %s", s.MountPoint)
	}
	if !info.IsDir() {
		return fmt.Errorf("mount point is not a directory: %s", s.MountPoint)
	}

	entries, err := os.ReadDir(s.MountPoint)
	if err != nil {
		return fmt.Errorf("failed to read mount point: %w", err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("SAFETY: mount point is not empty: %s", s.MountPoint)
	}

	// Verify not already mounted
	if s.checkMounted() {
		return fmt.Errorf("SAFETY: something is already mounted at: %s", s.MountPoint)
	}

	// Perform mount
	s.log("MOUNT", s.MountPoint)
	mountOpts := fmt.Sprintf("resvport,nolocks,vers=3,tcp,port=%d,mountport=%d", s.NFSPort, s.MountPort)
	cmd := exec.Command("sudo", "mount_nfs", "-o", mountOpts, "localhost:/", s.MountPoint)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mount failed: %w: %s", err, output)
	}

	// Verify mount succeeded
	time.Sleep(100 * time.Millisecond)
	if !s.checkMounted() {
		return fmt.Errorf("mount verification failed: not mounted")
	}

	s.IsMounted = true
	return nil
}

// checkMounted checks if something is mounted at the mount point.
func (s *SafeTestMount) checkMounted() bool {
	cmd := exec.Command("mount")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(output), " "+s.MountPoint+" ")
}

// Unmount performs a safe unmount with verification.
func (s *SafeTestMount) Unmount() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate path
	if err := s.validatePath(s.MountPoint); err != nil {
		return err
	}

	if !s.checkMounted() {
		s.IsMounted = false
		return nil // Already unmounted
	}

	s.log("UNMOUNT", s.MountPoint)

	// Try normal unmount first
	cmd := exec.Command("sudo", "umount", s.MountPoint)
	cmd.Run() // Ignore error, we'll check if still mounted

	time.Sleep(500 * time.Millisecond)

	// If still mounted, try force unmount
	if s.checkMounted() {
		cmd = exec.Command("sudo", "umount", "-f", s.MountPoint)
		cmd.Run()
		time.Sleep(500 * time.Millisecond)
	}

	// If still mounted, try diskutil on macOS
	if s.checkMounted() {
		cmd = exec.Command("sudo", "diskutil", "unmount", "force", s.MountPoint)
		cmd.Run()
		time.Sleep(500 * time.Millisecond)
	}

	// Final check
	if s.checkMounted() {
		return fmt.Errorf("failed to unmount: still mounted at %s", s.MountPoint)
	}

	s.IsMounted = false
	return nil
}

// Cleanup removes the mount point and marker file with all safety checks.
func (s *SafeTestMount) Cleanup() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate path
	if err := s.validatePath(s.MountPoint); err != nil {
		return err
	}

	// CRITICAL: Ensure not mounted before cleanup
	if s.checkMounted() {
		return fmt.Errorf("SAFETY: cannot cleanup - still mounted at %s", s.MountPoint)
	}

	s.log("CLEANUP", s.MountPoint)

	// Remove mount point if it exists
	if _, err := os.Stat(s.MountPoint); err == nil {
		cmd := exec.Command("sudo", "rm", "-rf", s.MountPoint)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to remove mount point: %w: %s", err, output)
		}
	}

	// Remove marker file if it exists (now in /tmp, no sudo needed)
	if _, err := os.Stat(s.MarkerFile); err == nil {
		os.Remove(s.MarkerFile)
	}

	// Verify cleanup
	if _, err := os.Stat(s.MountPoint); err == nil {
		return fmt.Errorf("cleanup verification failed: mount point still exists")
	}

	s.log("SESSION_END", s.SessionID)
	return nil
}

// MustCleanup is safe for use with defer - it logs errors but doesn't panic.
func (s *SafeTestMount) MustCleanup() {
	// Unmount first if needed
	if s.IsMounted {
		if err := s.Unmount(); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Unmount failed during cleanup: %v\n", err)
		}
	}

	// Then cleanup
	if err := s.Cleanup(); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: Cleanup failed: %v\n", err)
		s.log("CLEANUP_FAILED", fmt.Sprintf("%s: %v", s.MountPoint, err))
	}
}

// VerifyMounted returns an error if the mount is not active.
func (s *SafeTestMount) VerifyMounted() error {
	if !s.checkMounted() {
		return fmt.Errorf("not mounted at %s", s.MountPoint)
	}
	return nil
}

// SafePath returns a verified path under the mount point.
// It prevents path traversal attacks.
func (s *SafeTestMount) SafePath(relativePath string) (string, error) {
	// Clean the relative path
	cleanRel := filepath.Clean(relativePath)

	// Reject absolute paths
	if filepath.IsAbs(cleanRel) {
		return "", fmt.Errorf("relative path required, got absolute: %s", relativePath)
	}

	// Reject parent references
	if strings.HasPrefix(cleanRel, "..") || strings.Contains(cleanRel, "/../") {
		return "", fmt.Errorf("path traversal not allowed: %s", relativePath)
	}

	// Build full path
	fullPath := filepath.Join(s.MountPoint, cleanRel)

	// Verify it's still under mount point (defense in depth)
	if !strings.HasPrefix(fullPath, s.MountPoint+"/") && fullPath != s.MountPoint {
		return "", fmt.Errorf("path escape detected: %s resolved to %s", relativePath, fullPath)
	}

	return fullPath, nil
}
