// internal/utils/device_write_test.go
// Purpose: the hard-block rule against writing to a raw disk device must
// actually match a command that writes to a raw disk device.
//
// It did not, for as long as it existed. The pattern was written
// `>:\\s*/dev/sd[a-z]`, and in a Go raw string `\\s` is a literal backslash
// followed by 's' — so the rule demanded the text `>:\` before the device path,
// which no shell produces. `cat /dev/zero > /dev/sda` passed the hard blocker,
// and `echo x >/dev/sda` was additionally classified LOW risk by the analyser
// (fixed separately, in internal/commands/safety), which is the tier that does
// not ask for confirmation.
//
// This test asserts the rule by behaviour, not by pattern text, so a future
// rewrite of the expression cannot make it inert again without failing here.
package utils

import (
	"regexp"
	"testing"
)

func TestRawDeviceWritesAreRefused(t *testing.T) {
	blocked := []string{
		"cat /dev/zero > /dev/sda",
		"echo x >/dev/sda",
		"echo x > /dev/sdb1",
		"echo x >> /dev/sdc",
		"cat img > /dev/nvme0n1",
		"cat img >/dev/nvme1n2p3",
		"dd if=img of=x; echo y > /dev/hda",
		"echo x > /dev/vda",
		"cat img > /dev/disk2",
		"cat img > /dev/rdisk3",
		"CAT IMG > /DEV/SDA",
	}
	for _, cmd := range blocked {
		if err := ValidateCommand(cmd); err == nil {
			t.Errorf("ValidateCommand(%q) = nil, want a refusal — this writes to a raw "+
				"block device", cmd)
		}
	}
}

// The mirror, and the reason the pattern is not simply "/dev/": these are
// ordinary commands and must stay ordinary. A hard blocker that refuses
// `> /dev/null` breaks a large fraction of every shell script ever written.
func TestOrdinaryDeviceUseIsAllowed(t *testing.T) {
	allowed := []string{
		"echo x > /dev/null",
		"cat f 2>/dev/null",
		"ls -la /dev/sda",
		"lsblk /dev/nvme0n1",
		"cat /dev/urandom | head -c 10",
		"dd if=/dev/sda of=backup.img",
		"echo x > /dev/stdout",
		"sudo fdisk -l",
		"ls -la",
	}
	for _, cmd := range allowed {
		if err := ValidateCommand(cmd); err != nil {
			t.Errorf("ValidateCommand(%q) = %v, want nil — reading a device, or writing to "+
				"/dev/null, is not a disk overwrite", cmd, err)
		}
	}
}

// The precondition (§9 rule 8): the OLD pattern must genuinely have been unable
// to reach these commands, or the test above proves nothing about the fix.
//
// Rebuilt here rather than referenced, because the point is that the shipped
// expression was wrong: it matched only its own typo.
func TestTheOldDeviceWritePatternWasInert(t *testing.T) {
	const old = `>:\\s*/dev/sd[a-z]`
	shipped, err := regexp.Compile(old)
	if err != nil {
		t.Fatalf("the old pattern no longer compiles: %v", err)
	}

	for _, cmd := range []string{
		"cat /dev/zero > /dev/sda",
		"echo x >/dev/sda",
		"echo x > /dev/sdb1",
	} {
		if shipped.MatchString(cmd) {
			t.Fatalf("the old pattern matched %q — then the bug was something else and this "+
				"whole test file is describing the wrong thing", cmd)
		}
	}
	// What it DID match: the literal it was mis-escaped into asking for.
	if !shipped.MatchString(`echo x >:\s/dev/sda`) {
		t.Fatal("the old pattern did not even match its own typo; the diagnosis is wrong")
	}
}
