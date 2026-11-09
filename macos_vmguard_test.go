//go:build darwin

package absnfs

import (
	"os"
	"strings"
	"syscall"
	"testing"
)

// faultVMEnv must be 1 for a fault-injection mount test to run, and then
// only inside a macOS virtual machine (requireFaultInjectionVM).
const faultVMEnv = "ABSNFS_FAULT_INJECTION_VM"

// requireFaultInjectionVM skips the test unless it runs inside a macOS
// virtual machine and faultVMEnv is 1. It fails closed: anything it cannot
// read or recognise skips.
//
// Fault-injection tests make the server, or a tap in front of it, answer
// the macOS kernel's NFS client with injected failures. Here that is
// TestMacOSUndescribedEntry, whose backend fails Lstat with EIO. Such tests
// put the client into states that panicked the kernel on 2026-11-09 at 16:52
// ("corrupt list around element" in com.apple.filesystems.nfs, in the
// umount after a WRITE-failure case of a related harness). They must never
// run on a development machine, only in a disposable VM.
func requireFaultInjectionVM(t *testing.T) {
	t.Helper()
	if os.Getenv(faultVMEnv) != "1" {
		t.Skipf("fault injection against the kernel NFS client runs only in a disposable macOS VM with %s=1", faultVMEnv)
	}
	model, err := syscall.Sysctl("hw.model")
	if err != nil {
		t.Skipf("cannot read hw.model (%v): not running fault injection", err)
	}
	// Mac15,9 is the development machine whose kernel this panicked.
	if model == "Mac15,9" || !strings.HasPrefix(model, "VirtualMac") {
		t.Skipf("hw.model %q is not a macOS virtual machine: not running fault injection", model)
	}
	vmm, err := syscall.SysctlUint32("kern.hv_vmm_present")
	if err != nil || vmm != 1 {
		t.Skipf("kern.hv_vmm_present = %d (%v): not a virtual machine, not running fault injection", vmm, err)
	}
}
