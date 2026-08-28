// Package ambient implements BlackBox Phase 6 (optional): basic auditory
// awareness of the environment beyond speech — loud-noise, alarm-like,
// music-like, and silence events derived from the shared capture stream via
// rule-based analysis (RMS energy, spectral centroid). Classifier models
// (YAMNet-class) are explicitly deferred.
//
// Runs only in full voice mode AND when opted in. All categories respond with
// per-category cooldowns so Helix never loops ("are you okay?" spam).
//
// Skeleton compiled and tested since Phase 0.
package ambient

// Category classifies an ambient audio event.
type Category string

const (
	CategoryLoudNoise Category = "loud_noise"
	CategoryAlarmLike Category = "alarm_like"
	CategoryMusicLike Category = "music_like"
	CategorySilence   Category = "silence"
)

// ResponseMode governs what happens when a category fires.
type ResponseMode string

const (
	ResponseVocal  ResponseMode = "vocal"
	ResponseLog    ResponseMode = "log"
	ResponseIgnore ResponseMode = "ignore"
)

// Event is one detected ambient occurrence.
type Event struct {
	Category  Category
	Intensity float64 // 0..1 relative to configured sensitivity
}
