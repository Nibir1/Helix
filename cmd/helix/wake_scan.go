// cmd/helix/wake_scan.go
// Purpose: make the wake loop observable. Every wake failure used to reach
// `OnError: func(error) {}` — an empty callback in two places (here and
// internal/daemon/runtime.go) that looked wired and discarded everything: no
// recorder failure, device error, TCC denial or detector fault could reach a
// log, the screen, or the user. Since five consecutive capture failures end the
// scan loop (wakeword.maxConsecutiveScanErrs, ~8s at the shipped 1.5s chunk),
// the visible result was a shell that said "listening" over a dead microphone.
//
// Two channels, deliberately different:
//
//   - Failures are ALWAYS printed, deduplicated by message so a persistent
//     fault says its piece once instead of every 1.5 seconds. A broken
//     microphone is not debug detail.
//   - Per-chunk levels go to stderr under HELIX_DEBUG only. That is the
//     "why does speaking do nothing" instrument: it prints the chunk's RMS,
//     the level it was judged against, and the verdict, so a mis-set bar and
//     a silent recorder stop looking the same.
package main

import (
	"fmt"
	"os"
	"sync"

	"helix/internal/shell"
	"helix/internal/utils"
	"helix/internal/wakeword"
)

// wakeScanHooks builds the OnError/OnScan/capture-notice wiring shared by the
// interactive wake paths. Returned as a set because the dedup state is shared:
// a stream that cannot start reports through the capture notice AND makes every
// chunk slow, and the user needs to read that once, not twice.
type wakeScanHooks struct {
	OnError func(error)
	OnScan  func(wakeword.ScanReport)
	Notice  func(error)
}

// newWakeScanHooks returns hooks scoped to one wake service.
func newWakeScanHooks() wakeScanHooks {
	var mu sync.Mutex
	said := map[string]bool{}

	// once prints a message the first time this wake service produces it.
	once := func(label, msg string) {
		mu.Lock()
		if said[msg] {
			mu.Unlock()
			return
		}
		said[msg] = true
		mu.Unlock()
		fmt.Fprintln(os.Stderr, shell.Step(shell.StateWarn, label, msg))
	}

	return wakeScanHooks{
		OnError: func(err error) {
			if err == nil {
				return
			}
			once("wake listening problem", err.Error())
		},
		Notice: func(err error) {
			if err == nil {
				return
			}
			once("microphone capture degraded", err.Error())
		},
		OnScan: func(r wakeword.ScanReport) {
			if !utils.IsDebugMode() {
				return
			}
			if r.Err != nil {
				fmt.Fprintf(os.Stderr, "[wake] chunk FAILED (%d in a row): %v\n",
					r.Consecutive, r.Err)
				return
			}
			verdict := "quiet"
			switch {
			case r.Suppressed:
				verdict = "WOKE (suppressed by cooldown)"
			case r.Woke:
				verdict = "WOKE"
			}
			fmt.Fprintf(os.Stderr, "[wake] rms=%.5f  bar=%.5f  %s\n",
				r.RMS, r.Threshold, verdict)
		},
	}
}
