package metabolism

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"helix/internal/journal"
)

// FileName is the episode log inside the recorder's directory.
const FileName = "episodes.ndjson"

// Rotation bounds. Episodes are larger than voice lines (steps, usage), and
// losing one to rotation before it is ingested loses baseline data, so the
// budget is generous: 8 MiB × 5 generations is tens of thousands of turns.
const (
	defaultMaxBytes  int64 = 8 << 20
	defaultKeepFiles       = 4
)

// RepeatWindow is how soon a near-identical request must follow a turn for
// that turn to be labelled "repeated": the user asking again says the first
// attempt did not land, whatever its run end claimed.
const RepeatWindow = 10 * time.Minute

// UsageTotals are cumulative model-traffic counters. The recorder takes the
// difference across a turn.
type UsageTotals struct {
	Calls         int
	PromptChars   int64
	ResponseChars int64
}

// Options configures a Recorder. Usage and Declines are seams so this package
// stays free of the AI and prompt packages; nil means "not measured" (zero).
type Options struct {
	Dir      string // default: ~/.helix/metabolism
	Enabled  bool
	Usage    func() UsageTotals
	Declines func() int64
	Now      func() time.Time
	// MaxBytes/KeepFiles override the rotation budget (tests).
	MaxBytes  int64
	KeepFiles int
}

// Recorder writes episodes and outcomes. A nil *Recorder, or a disabled one,
// records nothing and every method is safe to call: call sites never need a
// nil check, and recording can never break the turn it describes.
type Recorder struct {
	mu       sync.Mutex
	dir      string
	app      *journal.Appender
	jopts    journal.Options
	usage    func() UsageTotals
	declines func() int64
	now      func() time.Time
	last     *lastTurn
}

// lastTurn is what repeat detection compares the next request against.
type lastTurn struct {
	id    string
	scope Scope
	text  string
	at    time.Time
}

// DefaultDir is ~/.helix/metabolism.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".helix", "metabolism"), nil
}

// Open prepares a recorder. It creates nothing on disk until the first record
// is written, and nothing at all while disabled.
func Open(o Options) (*Recorder, error) {
	if o.Dir == "" {
		d, err := DefaultDir()
		if err != nil {
			return nil, err
		}
		o.Dir = d
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = defaultMaxBytes
	}
	if o.KeepFiles <= 0 {
		o.KeepFiles = defaultKeepFiles
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	r := &Recorder{
		dir:      o.Dir,
		jopts:    journal.Options{MaxBytes: o.MaxBytes, KeepFiles: o.KeepFiles},
		usage:    o.Usage,
		declines: o.Declines,
		now:      o.Now,
	}
	if o.Enabled {
		if err := r.SetEnabled(true); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Enabled reports whether turns are being recorded.
func (r *Recorder) Enabled() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.app != nil
}

// SetEnabled starts or stops recording. Stopping keeps what was written.
func (r *Recorder) SetEnabled(on bool) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !on {
		r.app = nil
		r.last = nil
		return nil
	}
	if r.app != nil {
		return nil
	}
	// journal.Open makes the directory 0700 and deliberately not the file.
	app, err := journal.Open(filepath.Join(r.dir, FileName), r.jopts)
	if err != nil {
		return err
	}
	r.app = app
	return nil
}

// Path is the active log file.
func (r *Recorder) Path() string {
	if r == nil {
		return ""
	}
	return filepath.Join(r.dir, FileName)
}

// Turn accumulates one planner turn until Finish writes it.
type Turn struct {
	id        string
	started   time.Time
	scope     Scope
	usage0    UsageTotals
	declines0 int64
	steps     []Step
}

// ID is the episode ID this turn will be written under ("" for a nil turn).
// Helix stamps it on anything a later outcome must point back to, such as an
// undo journal entry.
func (t *Turn) ID() string {
	if t == nil {
		return ""
	}
	return t.id
}

// AddStep appends one executed step. Subject and error are scrubbed here, so a
// secret never sits in memory longer than it must.
func (t *Turn) AddStep(s Step) {
	if t == nil {
		return
	}
	s.Subject = cleanSubject(s.Subject)
	s.Err = cleanSubject(s.Err)
	t.steps = append(t.steps, s)
}

// Begin starts a turn in cwd. It returns nil while recording is off, and a nil
// Turn is safe to use.
func (r *Recorder) Begin(cwd string) *Turn {
	if !r.Enabled() {
		return nil
	}
	now := r.now()
	return &Turn{
		id:        NewID(now),
		started:   now,
		scope:     ScopeFor(cwd),
		usage0:    r.usageNow(),
		declines0: r.declinesNow(),
	}
}

// Finish writes the turn as an episode, the host's immediate outcome, and any
// outcomes the turn itself reveals: a declined confirmation during it, or the
// request being a repeat of the one just before.
func (r *Recorder) Finish(t *Turn, text, provenance, end string) {
	if t == nil || !r.Enabled() {
		return
	}
	now := r.now()
	u := r.usageNow()
	ep := Episode{
		ID:        t.id,
		Host:      HostName,
		Scope:     t.scope,
		StartedAt: t.started.UTC(),
		EndedAt:   now.UTC(),
		Request:   Request{Text: cleanText(text), Provenance: provenance},
		Steps:     t.steps,
		Usage: Usage{
			ModelCalls:  nonNeg(u.Calls - t.usage0.Calls),
			InputChars:  nonNeg(int(u.PromptChars - t.usage0.PromptChars)),
			OutputChars: nonNeg(int(u.ResponseChars - t.usage0.ResponseChars)),
		},
		End:   end,
		Attrs: map[string]string{"machine": MachineKey()},
	}
	r.write(Record{V: WireVersion, Kind: "episode", Episode: &ep})

	kind := OutcomeSuccess
	if end != EndDone {
		kind = OutcomeFailure
	}
	r.outcome(ep.ID, kind, SourceHost, "", now)

	if n := r.declinesNow() - t.declines0; n > 0 {
		note := "a confirmation was declined"
		if n > 1 {
			note = "confirmations were declined"
		}
		r.outcome(ep.ID, OutcomeDeclined, SourceUser, note, now)
	}

	r.mu.Lock()
	prev := r.last
	r.last = &lastTurn{id: ep.ID, scope: ep.Scope, text: ep.Request.Text, at: t.started}
	r.mu.Unlock()
	if prev != nil && prev.scope == ep.Scope && t.started.Sub(prev.at) <= RepeatWindow &&
		similar(prev.text, ep.Request.Text) {
		r.outcome(prev.id, OutcomeRepeated, SourceDerived, "asked again", now)
	}
}

// Outcome records a late signal about an earlier episode, such as an undo.
// An empty episode ID (the action predates recording) records nothing.
func (r *Recorder) Outcome(episodeID, kind, source, note string) {
	if episodeID == "" || !r.Enabled() {
		return
	}
	r.outcome(episodeID, kind, source, note, r.now())
}

func (r *Recorder) outcome(episodeID, kind, source, note string, at time.Time) {
	r.write(Record{V: WireVersion, Kind: "outcome", Outcome: &Outcome{
		ID: NewID(at), EpisodeID: episodeID, At: at.UTC(), Kind: kind, Source: source, Note: note,
	}})
}

func (r *Recorder) write(rec Record) {
	r.mu.Lock()
	app := r.app
	r.mu.Unlock()
	app.Append(rec) // nil-safe, best-effort, never returns an error
}

func (r *Recorder) usageNow() UsageTotals {
	if r.usage == nil {
		return UsageTotals{}
	}
	return r.usage()
}

func (r *Recorder) declinesNow() int64 {
	if r.declines == nil {
		return 0
	}
	return r.declines()
}

// nonNeg clamps a delta: /clear resets the usage meter, and a turn spanning a
// reset must not report negative cost.
func nonNeg(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// similar reports whether two requests are the same ask: identical after
// normalising case and punctuation, or sharing at least 80% of their words
// (for requests of three words or more). Deliberately strict: a false
// "repeated" blames a turn that worked.
func similar(a, b string) bool {
	wa, wb := words(a), words(b)
	if len(wa) == 0 || len(wb) == 0 {
		return false
	}
	if strings.Join(wa, " ") == strings.Join(wb, " ") {
		return true
	}
	if len(wa) < 3 || len(wb) < 3 {
		return false
	}
	set := map[string]bool{}
	for _, w := range wa {
		set[w] = true
	}
	inter, union := 0, len(set)
	seen := map[string]bool{}
	for _, w := range wb {
		if seen[w] {
			continue
		}
		seen[w] = true
		if set[w] {
			inter++
		} else {
			union++
		}
	}
	return float64(inter)/float64(union) >= 0.8
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
}
