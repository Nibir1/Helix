// internal/shell/foreign.go
// Purpose: framing output that is not Helix's.
//
// THE PROBLEM. A package install prints two Helix lines, then dumps the package
// manager's output at column zero, then prints two more Helix lines:
//
//	│ ! piper-local  not installed — installing it
//	│   → pip3 install --user piper-tts
//
//	Collecting piper-tts
//	  Downloading piper_tts-1.2.0-py3-none-any.whl (58 kB)
//	Successfully installed piper-tts-1.2.0
//
//	│ ✔ piper-local  installed
//
// Everything between the gutters is pip talking, and nothing says so. When it
// succeeds that is merely untidy; when it fails, a stranger's error text sits
// in the middle of Helix's own report with no indication of who is speaking,
// and the reader has to work out which lines they can act on. The same applies
// to cargo, brew, apt and every model download.
//
// Helix does not reformat that output — it cannot, and reflowing a progress bar
// would be worse than leaving it. What it can do is say where the handover
// happens, in both directions.
package shell

import "strings"

// glyphForeign marks the boundary of foreign output: a light vertical, visibly
// not the solid gutter Helix's own lines carry.
const (
	glyphForeignOpen  = "╷"
	glyphForeignClose = "╵"
)

// ForeignOpen announces that everything after it comes from source.
//
// source is the program, not the operation: "pip3", not "installing piper".
// The reader's question at a wall of unfamiliar text is who is talking.
func ForeignOpen(source string) string {
	return "  " + Fg(HexSubtle, glyphForeignOpen+" ") +
		Muted("output below is ") + Fg(HexTertiary, source) + Muted("'s own")
}

// ForeignClose marks the end of it, and says how it went in the same breath so
// a long scroll does not have to be read backwards to find out.
func ForeignClose(source string, ok bool) string {
	verdict, colour := "finished", HexMuted
	if !ok {
		verdict, colour = "failed", HexRectifier
	}
	return "  " + Fg(HexSubtle, glyphForeignClose+" ") +
		Fg(HexTertiary, source) + " " + Fg(colour, verdict)
}

// SourceOf names the program a command line will run, for the frame labels.
//
// The first field, with any directory stripped: "/usr/bin/pip3 install x" is
// pip3 talking. An empty command yields "the command", which reads correctly in
// the frame and is better than an empty label.
func SourceOf(cmdLine string) string {
	fields := strings.Fields(cmdLine)
	if len(fields) == 0 {
		return "the command"
	}

	// Step past the wrappers. "sudo apt-get install -y sox" is apt-get talking;
	// labelling the frame "sudo" names the thing that granted permission rather
	// than the thing producing the output, which is the one question the label
	// exists to answer.
	i := 0
	for i < len(fields) {
		switch base(fields[i]) {
		case "sudo", "doas", "env", "nice", "command", "exec", "time", "stdbuf":
			i++
			// Their own flags and any VAR=value assignments belong to the
			// wrapper, not to the program being wrapped.
			for i < len(fields) {
				f := fields[i]
				switch {
				case strings.HasPrefix(f, "-"):
					i++
					// A flag that takes a value swallows the next field too,
					// or "sudo -u nobody apt-get" reports the USER as the
					// program. The set is sudo's and doas's; an unknown flag
					// is assumed to take none, which fails towards naming the
					// wrong program rather than skipping past the right one.
					if valueTakingWrapperFlag[f] && i < len(fields) {
						i++
					}
				case strings.Contains(f, "="):
					i++
				default:
					goto wrapperDone
				}
			}
		wrapperDone:
			continue
		}
		break
	}
	if i >= len(fields) {
		// Nothing but wrappers: name the first thing rather than nothing.
		i = 0
	}

	prog := base(fields[i])
	// A bare interpreter says nothing useful; the module after -m does.
	// "python3 -m pip install x" is pip talking.
	if (prog == "python" || prog == "python3" || prog == "py") &&
		i+2 < len(fields) && fields[i+1] == "-m" {
		return fields[i+2]
	}
	return prog
}

// valueTakingWrapperFlag lists the sudo/doas flags whose value is a separate
// field.
var valueTakingWrapperFlag = map[string]bool{
	"-u": true, "-g": true, "-p": true, "-C": true, "-h": true,
	"-r": true, "-t": true, "-T": true, "-U": true,
}

// base strips any directory from a program path.
func base(prog string) string {
	if i := strings.LastIndexAny(prog, `/\`); i >= 0 && i+1 < len(prog) {
		return prog[i+1:]
	}
	return prog
}
