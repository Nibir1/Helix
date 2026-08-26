// internal/shell/prompt.go
// Purpose: SYNAPSE-styled native shell prompt renderer with TrueColor panels,
// width-safe glyphs, and a lightweight glitch API for the animation engine.
//
// COLOR BALANCE SYSTEM (Helix palette):
//
//	Cyan        = identity / brand panels (left brand + right "Helix")
//	Magenta     = context panels (left CWD + right user name)
//	Red         = crew / interactive accents (right "Red Team" + left ❯)
//	Grid+Orange = telemetry panels (git branch + clock)
//	Void        = base / text-on-bright panels
package shell

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

// Helix Color Palette (Tron / Red Team)
const (
	// Helix identity — the brand, unchanged. These are what the banner, the
	// prompt blocks and the state badges are built from.
	HexPrimary   = "#04D9FF" // Electric Cyan
	HexSecondary = "#FF0055" // Neon Magenta
	HexRectifier = "#FF0000" // Aggressive Red
	HexVoid      = "#030504" // Near-black (Tron poster black)
	HexGrid      = "#1A1A1A" // Dim Grey
	HexText      = "#FAFAFA" // White

	// The reading layer, drawn from the Tron Legacy poster palette
	// (#193f4a / #2f8ca3 / #f4af2d / #fffffe / #030504) and lifted along its
	// own hue until it is actually legible on a dark terminal.
	//
	// This is a CONTRAST FIX, not a repaint. HexSubtle was #444444 — a flat
	// grey measuring 1.44:1 against an ordinary dark background, where WCAG's
	// floor for body text is 4.5:1 and even large text wants 3:1. It was
	// carrying two incompatible jobs: panel rules and gutters, which SHOULD
	// recede, and prose and labels, which must be readable. At one value the
	// readable half lost, so /about rendered its philosophy in text that was
	// very nearly invisible.
	//
	// Split in two, each measured against #282C34 (a common dark theme, and
	// the lighter of the plausible backgrounds — so these numbers are the
	// worst case, not the flattering one):
	HexSubtle = "#2C6E82" // 2.44:1 — chrome only: rules, gutters, list numbers
	HexMuted  = "#4FB8D4" // 6.09:1 — secondary TEXT: prose, labels, headers
	HexAmber  = "#F4AF2D" // 7.34:1 — Tron gold, the value colour
	HexTeal   = "#193F4A" // the poster's deep teal, for filled blocks

	// HexTertiary is kept as the warning hue. It reads at 6.54:1, and orange
	// carries "caution" in a way the gold does not.
	HexTertiary = "#FF9900" // Orange
)

// Width-safe glyphs (render 1-cell in every terminal).
const (
	GlyphPrompt = "❯"
	GlyphDirty  = "●"
)

// PromptContext holds dynamic data for the prompt.
type PromptContext struct {
	CWD         string
	GitBranch   string
	GitDirty    bool
	AIStatus    string
	IsTransient bool
}

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// identityName is the user's configured name (defaults to the creator).
var identityName = "Nahasat Nibir"

// SetUserName updates the identity name displayed in the right prompt.
func SetUserName(name string) {
	if strings.TrimSpace(name) != "" {
		identityName = strings.TrimSpace(name)
	}
}

// GetContext gathers CWD and Git info once per prompt (never per frame).
func GetContext() PromptContext {
	ctx := PromptContext{AIStatus: "Helix // Red Team // " + identityName}
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(cwd, home) {
		ctx.CWD = "~" + cwd[len(home):]
	} else {
		ctx.CWD = filepath.Base(cwd)
	}
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err == nil {
		branch := strings.TrimSpace(string(out))
		if branch != "" && branch != "HEAD" {
			ctx.GitBranch = branch
			statusCmd := exec.Command("git", "status", "--porcelain")
			statusOut, _ := statusCmd.Output()
			ctx.GitDirty = len(strings.TrimSpace(string(statusOut))) > 0
		}
	}
	return ctx
}

// Glitch randomly replaces characters with sci-fi symbols for persistent
// UI panels. User input and AI output are never glitched.
func Glitch(text string, prob float64) string {
	if prob <= 0 {
		return text
	}
	glitchChars := []rune("ΞΣΛΩΓΔΦΨΠθ?@#%&")
	runes := []rune(text)
	for i := range runes {
		if runes[i] == ' ' {
			continue
		}
		if rand.Float64() < prob {
			runes[i] = glitchChars[rand.Intn(len(glitchChars))]
		}
	}
	return string(runes)
}

func hexToRGB(hex string) (int, int, int) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 255, 255, 255
	}
	r, _ := strconv.ParseInt(hex[0:2], 16, 32)
	g, _ := strconv.ParseInt(hex[2:4], 16, 32)
	b, _ := strconv.ParseInt(hex[4:6], 16, 32)
	return int(r), int(g), int(b)
}

func fgHex(hex string) string {
	r, g, b := hexToRGB(hex)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}

func bgHex(hex string) string {
	r, g, b := hexToRGB(hex)
	return fmt.Sprintf("\033[48;2;%d;%d;%dm", r, g, b)
}

const ansiReset = "\033[0m"
const bold = "\033[1m"

// Fg wraps s in an exact-hex foreground color.
func Fg(hex, s string) string { return fgHex(hex) + s + ansiReset }

// Bg wraps s in an exact-hex background color.
func Bg(hex, s string) string { return bgHex(hex) + s + ansiReset }

// Seg renders s with an exact-hex background AND foreground panel.
func Seg(bg, fg, s string) string { return bgHex(bg) + fgHex(fg) + s + ansiReset }

// visibleWidth strips ANSI and returns the true terminal cell width.
func visibleWidth(s string) int {
	return runewidth.StringWidth(ansiRegex.ReplaceAllString(s, ""))
}

// TerminalWidth returns the current terminal column count.
// Probes stdout, stdin, and the controlling terminal; keeps the largest
// valid answer so wrapped launchers never report a stale width.
func TerminalWidth() int {
	best := 0
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > best {
		best = w
	}
	if w, _, err := term.GetSize(int(os.Stdin.Fd())); err == nil && w > best {
		best = w
	}
	if runtime_isUnix() {
		if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
			if w, _, ioErr := term.GetSize(int(tty.Fd())); ioErr == nil && w > best {
				best = w
			}
			_ = tty.Close()
		}
	}
	return best
}

func runtime_isUnix() bool {
	switch goos() {
	case "windows":
		return false
	default:
		return true
	}
}

// RenderPromptParts builds the left/right prompt panels and their exact
// visible widths. glitchProb controls the cyberpunk flicker intensity.
func RenderPromptParts(ctx PromptContext, glitchProb float64) (left, right string, leftLen, rightLen int) {
	if ctx.IsTransient {
		left = Fg(HexRectifier, bold+GlyphPrompt) + " "
		return left, "", visibleWidth(left), 0
	}

	var b strings.Builder

	// Identity panel — Electric Cyan block (left brand)
	b.WriteString(bgHex(HexPrimary))
	b.WriteString(bold)
	b.WriteString(fgHex(HexVoid))
	b.WriteString(" HELIX ")
	b.WriteString(ansiReset)
	b.WriteString(" ")

	// Context panel — Neon Magenta block (glitched)
	b.WriteString(Seg(HexSecondary, HexText, " "+Glitch(ctx.CWD, glitchProb)+" "))

	if ctx.GitBranch != "" {
		b.WriteString(" ")
		// Telemetry panel — Grid block, orange branch, red dirty dot
		b.WriteString(bgHex(HexGrid))
		b.WriteString(fgHex(HexTertiary))
		b.WriteString(" ")
		b.WriteString(Glitch(ctx.GitBranch, glitchProb))
		if ctx.GitDirty {
			b.WriteString(" ")
			b.WriteString(Fg(HexRectifier, GlyphDirty))
		}
		b.WriteString(" " + ansiReset)
	}

	// Interactive accent — Red prompt symbol
	b.WriteString(" ")
	b.WriteString(fgHex(HexRectifier))
	b.WriteString(bold)
	b.WriteString(GlyphPrompt)
	b.WriteString(ansiReset)
	b.WriteString(" ")

	left = b.String()
	leftLen = visibleWidth(left)

	// ── RIGHT PROMPT ─────────────────────────────────────────────
	var r strings.Builder

	// Telemetry panel — Grid block, orange clock
	r.WriteString(Seg(HexGrid, HexTertiary, " "+time.Now().Format("15:04:05")+" "))
	r.WriteString(" ")

	// Identity ribbon — TRI-COLOR mix with balanced Helix palette:
	//   "Helix"    → Electric Cyan bg / Void text, bold  (brand)
	//   "Red Team" → Aggressive Red bg / White text      (crew)
	//   user name  → Neon Magenta bg / White text        (identity)
	// joined by dim Grid "//" connector chips.
	parts := strings.Split(ctx.AIStatus, "//")
	if len(parts) == 3 {
		brand := strings.TrimSpace(parts[0])
		crew := strings.TrimSpace(parts[1])
		name := strings.TrimSpace(parts[2])
		conn := Seg(HexGrid, HexPrimary, " ❯ ")

		var seg strings.Builder
		seg.WriteString(bgHex(HexPrimary))
		seg.WriteString(bold)
		seg.WriteString(fgHex(HexVoid))
		seg.WriteString(" ")
		seg.WriteString(Glitch(brand, glitchProb))
		seg.WriteString(" ")
		seg.WriteString(ansiReset)
		r.WriteString(seg.String())
		// r.WriteString(conn)
		r.WriteString(Seg(HexRectifier, HexText, " "+Glitch(crew, glitchProb)+" "))
		r.WriteString(conn)
		r.WriteString(Seg(HexSecondary, HexText, " "+Glitch(name, glitchProb)+" "))
	} else {
		// Fallback: single brand block if the status format ever changes.
		r.WriteString(Seg(HexPrimary, HexVoid, " "+Glitch(ctx.AIStatus, glitchProb)+" "))
	}

	right = r.String()
	rightLen = visibleWidth(right)
	return
}

// PrintTransient replaces the previous prompt with a minimal one (p10k transient).
func PrintTransient(input string) {
	fmt.Printf("\033[1A\033[2K%s%s\n", Fg(HexRectifier, bold+GlyphPrompt)+" ", input)
}

func goos() string { return runtime.GOOS }
