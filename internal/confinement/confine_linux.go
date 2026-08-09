//go:build linux

// internal/confinement/confine_linux.go
// Purpose: Linux kernel confinement. Prefers bubblewrap (namespace-based, no
// code trust); falls back to the Landlock LSM implemented with raw syscalls
// via golang.org/x/sys/unix (CGO-free, no new dependencies). Landlock is
// self-imposing, so confinement re-execs `helix --confined-child`, which
// applies the ruleset to itself and then runs the shell.
// Dependencies: golang.org/x/sys/unix (existing), stdlib.
package confinement

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Landlock syscall numbers (generic ABI, amd64/arm64) and rule type.
const (
	landlockRulePathBeneath = 1
	// LANDLOCK_ACCESS_FS_* rights (bit positions from the UAPI).
	accExecute   = 1 << 0
	accWriteFile = 1 << 1
	accReadFile  = 1 << 2
	accReadDir   = 1 << 3
	accRemoveDir = 1 << 4
	accRemoveFl  = 1 << 5
	accMakeChar  = 1 << 6
	accMakeDir   = 1 << 7
	accMakeReg   = 1 << 8
	accMakeSock  = 1 << 9
	accMakeFifo  = 1 << 10
	accMakeBlock = 1 << 11
	accMakeSym   = 1 << 12
	accRefer     = 1 << 13
	accTruncate  = 1 << 14
)

var (
	accessFSRead = uint64(accExecute | accReadFile | accReadDir)
	accessFSAll  = accessFSRead | uint64(accWriteFile|accRemoveDir|accRemoveFl|
		accMakeChar|accMakeDir|accMakeReg|accMakeSock|accMakeFifo|
		accMakeBlock|accMakeSym|accRefer|accTruncate)
)

func detectBackend() Backend {
	if lookPath("bwrap") && bwrapWorks() {
		return BackendBwrap
	}
	if landlockSupported() {
		return BackendLandlock
	}
	return BackendNone
}

// bwrapWorks probes that bubblewrap can actually create a sandbox here
// (user-namespace restrictions vary by distro/CI).
func bwrapWorks() bool {
	tmp, err := os.MkdirTemp("", "helix-bwrap-probe-*")
	if err != nil {
		return false
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	return exec.Command("bwrap", "--ro-bind", "/", "/", "--bind", tmp, tmp,
		"--proc", "/proc", "--dev", "/dev", "/bin/true").Run() == nil
}

// landlockSupported probes the ABI version via a NULL create_ruleset call.
func landlockSupported() bool {
	v, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, 0)
	return errno == 0 && v > 0
}

func wrapCommand(argv []string, p Profile) ([]string, bool) {
	switch Detect() {
	case BackendBwrap:
		return append(BuildBwrapArgs(p), argv...), true
	case BackendLandlock:
		exe, err := os.Executable()
		if err != nil {
			return nil, false
		}
		out, err := BuildLandlockChildArgs(exe, p, argv)
		if err != nil {
			return nil, false
		}
		return out, true
	}
	return nil, false
}

// RunConfinedChild is the Landlock re-exec entrypoint:
// args = <specJSON> -- <cmd> [cmdArgs...]. It confines ITSELF then runs cmd.
//
// Args: args: post-flag argv. Returns: child exit code.
// Complexity: O(rules) + command runtime.
func RunConfinedChild(args []string) int {
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 1 || sep == len(args)-1 {
		fmt.Fprintln(os.Stderr, "helix: malformed --confined-child arguments")
		return 2
	}
	var spec childSpec
	if err := json.Unmarshal([]byte(args[0]), &spec); err != nil {
		fmt.Fprintf(os.Stderr, "helix: bad confinement spec: %v\n", err)
		return 2
	}
	if err := applyLandlock(Profile{Root: spec.Root, ExtraRW: spec.ExtraRW}); err != nil {
		fmt.Fprintf(os.Stderr, "helix: landlock confinement failed: %v\n", err)
		return 1
	}
	cmd := exec.Command(args[sep+1], args[sep+2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if spec.Cwd != "" {
		cmd.Dir = spec.Cwd
	}
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return 1
	}
	return 0
}

// applyLandlock installs a Landlock ruleset on the calling process:
// read/execute everywhere, full read-write under the jail root (+extras).
//
// Args: p: resolved profile. Returns: error when the kernel rejects us.
// Complexity: O(number of rules).
func applyLandlock(p Profile) error {
	attr := make([]byte, 8) // struct landlock_ruleset_attr (ABI1: handled_access_fs)
	binary.LittleEndian.PutUint64(attr, accessFSAll)
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(&attr[0])), uintptr(len(attr)), 0)
	if errno != 0 {
		return fmt.Errorf("create_ruleset: %w", errno)
	}
	defer unix.Close(int(fd))

	if err := addPathRule(int(fd), "/", accessFSRead); err != nil {
		return err
	}
	for _, dir := range append([]string{p.Root}, p.ExtraRW...) {
		if err := addPathRule(int(fd), dir, accessFSAll); err != nil {
			return err
		}
	}
	// prctl takes 5 arguments, so we must use Syscall6 (trap + 6 args).
	if _, _, errno := unix.Syscall6(unix.SYS_PRCTL, uintptr(unix.PR_SET_NO_NEW_PRIVS), 1, 0, 0, 0, 0); errno != 0 {
		return fmt.Errorf("prctl(no_new_privs): %w", errno)
	}
	// landlock_restrict_self takes 2 arguments (fd, flags), so we use standard Syscall (trap + 3 args).
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0); errno != 0 {
		return fmt.Errorf("restrict_self: %w", errno)
	}
	return nil
}

// addPathRule attaches a path-beneath rule. The kernel UAPI struct is packed
// (12 bytes: u64 access + s32 fd), so we encode it manually to avoid Go padding.
func addPathRule(rulesetFd int, path string, access uint64) error {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer unix.Close(fd)
	attr := make([]byte, 12)
	binary.LittleEndian.PutUint64(attr[:8], access)
	binary.LittleEndian.PutUint32(attr[8:12], uint32(fd))
	// landlock_add_rule takes 4 arguments, so we must use Syscall6 (trap + 6 args).
	if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE,
		uintptr(rulesetFd), uintptr(landlockRulePathBeneath),
		uintptr(unsafe.Pointer(&attr[0])), 0, 0, 0); errno != 0 {
		return fmt.Errorf("add_rule %s: %w", path, errno)
	}
	return nil
}
