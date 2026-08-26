// cmd/helix/config_keys_test.go
// Purpose: every "/config <key>" Helix prints must be a key /config accepts.
//
// /config takes a fixed allowlist of short names, and nothing connected that
// list to the strings telling people what to type. The INTERRUPT row shipped
// saying "/config speech.tts.barge_in true" — the JSON path from config.json,
// not a /config key — so the single instruction Helix gave for enabling
// barge-in was a command it would reject. Caught by a user reading the output,
// which is the expensive way to find it.
//
// This is the same guard as the README command-reference drift test, one layer
// down: the registry stops /help drifting from the code, and this stops printed
// guidance drifting from the allowlist.
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// configRefRe finds a /config reference followed by a key, inside a Go string.
var configRefRe = regexp.MustCompile(`/config ([a-z][a-z0-9._-]*)`)

// prosePreposition lists words that follow /config in a sentence rather than as
// an argument. Deliberately tiny: a long stoplist would start swallowing real
// key names.
var prosePreposition = map[string]bool{
	"with": true, "to": true, "and": true, "or": true,
	"the": true, "no": true, "in": true, "for": true,
}

func TestPrintedConfigKeysExist(t *testing.T) {
	valid := map[string]bool{}
	for _, k := range configKeys() {
		valid[k.name] = true
	}
	if len(valid) == 0 {
		t.Fatal("no config keys registered")
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, rerr := os.ReadFile(f)
		if rerr != nil {
			t.Fatalf("read %s: %v", f, rerr)
		}
		for i, line := range strings.Split(string(src), "\n") {
			trimmed := strings.TrimSpace(line)
			// Comments describe history and may name keys that are long gone.
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, m := range configRefRe.FindAllStringSubmatch(line, -1) {
				key := m[1]
				// "/config <key [value]>" and friends are usage syntax, not a key.
				if strings.ContainsAny(key, "<[") {
					continue
				}
				// Prose uses /config as a noun ("Run /config with no argument
				// to list every settable key"). A following function word is
				// never a key name, and the alternative — matching only
				// command-shaped lines — misses the real cases this exists for.
				if prosePreposition[key] {
					continue
				}
				if !valid[key] {
					t.Errorf("%s:%d tells the user to run %q, but /config has no key %q\n  %s",
						f, i+1, m[0], key, trimmed)
				}
			}
		}
	}
}

// The two keys the voice panels point at must keep existing, since they are the
// only instructions given for features that are otherwise invisible.
func TestVoicePanelConfigKeysAreRegistered(t *testing.T) {
	valid := map[string]bool{}
	for _, k := range configKeys() {
		valid[k.name] = true
	}
	for _, key := range []string{"barge-in", "context-turns"} {
		if !valid[key] {
			t.Errorf("/config %s is referenced by a status panel but not registered", key)
		}
	}
}
