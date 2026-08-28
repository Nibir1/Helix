// cmd/helix/voice_stats.go
//
// Purpose: BlackBox P3.5 — the metrics display deferred in Phase 3 with the note
// "the file is plain JSONL".
//
// That note was true and it was not enough. Four metrics files have been written
// since Phase 3 and nothing has ever read one, which is why P7.8 — "metrics
// collection run against the §10 table", one of the two items gating the release
// tag — had no tooling: the numbers existed and the only way to see them was to
// `cat` NDJSON and do the arithmetic by hand.
//
// The report's job is to be honest rather than impressive. Three rules follow
// from that, and each one costs a number the report could otherwise have shown:
//
//  1. Judge each sample against the column of §10 it belongs to. A local whisper
//     turn is measured against the 6s local budget, not the 3s cloud one, and
//     the provider recorded with the sample is what decides which.
//  2. Never print a percentile a sample cannot support. Below 20 samples the
//     "p95" is the maximum in a costume, and a release decision should not rest
//     on it.
//  3. Say "not measured" and mean it. An absent file is the normal state of a
//     fresh install, and a target with no samples must read as unevaluated —
//     never as a pass.
package main

import (
	"fmt"
	"time"

	"helix/internal/metrics"
	"helix/internal/shell"
)

// handleVoiceStatsCommand implements /blackbox stats.
func handleVoiceStatsCommand() {
	dir, err := metrics.Dir()
	if err != nil {
		uiFail("metrics directory", err.Error())
		return
	}

	wake := loadMetric(dir, metrics.FileWake)
	voice := loadMetric(dir, metrics.FileVoice)
	vision := loadMetric(dir, metrics.FileVision)
	speechRecs := loadMetric(dir, metrics.FileSpeech)
	ambient := loadMetric(dir, metrics.FileAmbient)

	uptime := loadMetric(dir, metrics.FileUptime)

	if len(wake)+len(voice)+len(vision)+len(speechRecs)+len(ambient)+len(uptime) == 0 {
		fmt.Println(shell.PanelTitle("voice metrics"))
		for _, l := range shell.PanelWrap(
			"nothing measured yet. Metrics are written as you use live mode — wake "+
				"events, voice turns, spoken replies and camera looks each leave a "+
				"local sample in ~/.helix/metrics.", shell.Muted) {
			fmt.Println(l)
		}
		fmt.Println(shell.PanelEnd())
		return
	}

	fmt.Println(shell.PanelTitle("voice metrics"))
	for _, l := range shell.PanelWrap(
		"measured on this machine, against the §10 targets. local and cloud paths "+
			"are judged separately, because they have different budgets.", shell.Muted) {
		fmt.Println(l)
	}
	fmt.Println(shell.PanelGap())

	rows := [][]string{}
	rows = append(rows, latencyRows(metrics.MetricWakeToExec, "wake → execution",
		metrics.Filter(voice, metrics.MetricWakeToExec), sttProviderOf)...)
	rows = append(rows, latencyRows(metrics.MetricFirstAudio, "TTS first audio",
		metrics.Filter(speechRecs, metrics.MetricFirstAudio), providerOf)...)
	rows = append(rows, latencyRows(metrics.MetricFrameToInsight, "frame → insight",
		metrics.Filter(vision, metrics.MetricFrameToInsight), providerOf)...)

	if len(rows) == 0 {
		fmt.Println(shell.PanelLine(shell.Muted("no latency samples recorded yet")))
	} else {
		for _, l := range shell.Table(
			[]string{"metric", "path", "n", "p50", "worst", "target", ""}, rows) {
			fmt.Println(l)
		}
	}
	fmt.Println(shell.PanelEnd())

	printWakePanel(wake, voice)
	printDaemonPanel(uptime)
	printAmbientPanel(ambient)

	fmt.Println(shell.Hint("verdicts compare the p50 against §10 · " +
		"samples in ~/.helix/metrics · /purge wipes them"))
}

// loadMetric reads one file, reporting a read error without aborting the report.
func loadMetric(dir, name string) []metrics.Record {
	recs, err := metrics.Load(dir, name)
	if err != nil {
		uiWarn(name+" metrics", err.Error())
		return nil
	}
	return recs
}

func providerOf(r metrics.Record) string    { return r.Provider }
func sttProviderOf(r metrics.Record) string { return r.STTProvider }

// latencyRows splits a metric's samples by local/cloud path and renders one row
// per path that has samples.
//
// Splitting is the point rather than a nicety: §10 sets different budgets for
// the two, so a mixed p50 would be measured against a threshold that applies to
// neither half of it.
func latencyRows(metric, label string, recs []metrics.Record,
	provider func(metrics.Record) string) [][]string {
	if len(recs) == 0 {
		return [][]string{{
			shell.Value(label), shell.Muted("—"), shell.Muted("0"),
			shell.Muted("—"), shell.Muted("—"), shell.Muted("—"),
			shell.Badge(shell.StateIdle, "not measured"),
		}}
	}

	var local, cloud []metrics.Record
	for _, r := range recs {
		if metrics.IsLocal(provider(r)) {
			local = append(local, r)
		} else {
			cloud = append(cloud, r)
		}
	}

	var out [][]string
	for _, group := range []struct {
		name    string
		isLocal bool
		recs    []metrics.Record
	}{{"cloud", false, cloud}, {"local", true, local}} {
		if len(group.recs) == 0 {
			continue
		}
		sum := metrics.SummarizeLatency(group.recs)
		if sum.N == 0 {
			continue
		}
		limit, ok := metrics.Verdict(metric, group.isLocal,
			time.Duration(sum.P50)*time.Millisecond)

		target := shell.Muted("—")
		verdict := shell.Badge(shell.StateIdle, "no target")
		if limit > 0 {
			target = shell.Muted(metrics.FormatMs(limit.Milliseconds()))
			switch {
			case !ok:
				verdict = shell.Badge(shell.StateBad, "over target")
			case sum.Max > limit.Milliseconds():
				// The typical turn meets the budget and the worst one does not.
				// Reporting a flat "meets target" here would hide exactly the
				// case a release decision needs to see, so the verdict names the
				// statistic it is about — the first version of this report said
				// "meets target" beside a visible 6.80s worst case against a
				// 6.00s budget, which is true and reads as misleading.
				verdict = shell.Badge(shell.StateWarn, "typical only")
			default:
				verdict = shell.Badge(shell.StateGood, "meets target")
			}
		}

		// The worst case is shown as a p95 only when the sample supports one;
		// otherwise it is labelled as the maximum, which is what it is.
		worst := metrics.FormatMs(sum.Max) + shell.Muted(" max")
		if sum.P95Meaningful() {
			worst = metrics.FormatMs(sum.P95) + shell.Muted(" p95")
		}

		out = append(out, []string{
			shell.Value(label),
			shell.Muted(group.name),
			shell.Muted(fmt.Sprint(sum.N)),
			shell.Value(metrics.FormatMs(sum.P50)),
			worst,
			target,
			verdict,
		})
	}
	return out
}

// printWakePanel reports hands-free triggering, including the honest caveat on
// the false-positive proxy.
func printWakePanel(wake, voice []metrics.Record) {
	if len(wake) == 0 {
		return
	}
	st := metrics.SummarizeWake(wake, voice)

	w := shell.KVWidth("EVENTS", "RATE", "UNANSWERED")
	fmt.Println(shell.PanelTitle("wake word"))
	fmt.Println(shell.KV("EVENTS", shell.Value(fmt.Sprint(st.Events)), w))

	if st.WindowKnown {
		rate := fmt.Sprintf("%.2f/hour", st.PerHour)
		over := st.PerHour > 1
		state := shell.StateGood
		if over {
			state = shell.StateWarn
		}
		fmt.Println(shell.KV("RATE", shell.Badge(state, rate)+
			shell.Muted(fmt.Sprintf("  over %s  ·  §10 target ≤1/hour false positives",
				st.Window.Round(time.Minute))), w))
	} else {
		fmt.Println(shell.KV("RATE", shell.Muted(
			"not computable — needs two timestamped events"), w))
	}

	fmt.Println(shell.KV("UNANSWERED", shell.Value(fmt.Sprint(st.Unanswered))+
		shell.Muted("  wakes with no turn within 60s"), w))
	for _, l := range shell.PanelWrap(
		"unanswered is a PROXY, not the §10 false-positive rate: Helix cannot know "+
			"whether you meant to wake it, so a wake with no turn is either a false "+
			"trigger or a change of mind. Treat the rate above as an upper bound.",
		shell.Muted) {
		fmt.Println(l)
	}
	fmt.Println(shell.PanelEnd())
}

// printDaemonPanel reports daemon availability against the 99.5% target.
//
// Availability is observed heartbeats over expected ones, which is the only
// honest reading of an in-band measurement: a dead daemon writes nothing, so
// downtime is absence. The longest gap is shown beside the percentage because a
// percentage cannot distinguish one long outage from hundreds of brief ones, and
// 99.5% of 72 hours is 21 minutes either way.
func printDaemonPanel(uptime []metrics.Record) {
	if len(uptime) == 0 {
		return
	}
	av := metrics.SummarizeAvailability(uptime)

	w := shell.KVWidth("SAMPLES", "AVAILABILITY", "RESTARTS", "LONGEST GAP")
	fmt.Println(shell.PanelTitle("daemon"))
	fmt.Println(shell.KV("SAMPLES", shell.Value(fmt.Sprint(av.Samples))+
		shell.Muted(fmt.Sprintf("  of %d expected  ·  over %s",
			av.Expected, av.Window.Round(time.Minute))), w))

	if av.Expected <= 1 {
		// One heartbeat proves it ran; it cannot support a percentage.
		fmt.Println(shell.KV("AVAILABILITY", shell.Badge(shell.StateIdle, "not computable")+
			shell.Muted("  needs a longer window"), w))
	} else {
		state := shell.StateGood
		if av.Percent < metrics.UptimeTarget {
			state = shell.StateBad
		}
		fmt.Println(shell.KV("AVAILABILITY",
			shell.Badge(state, fmt.Sprintf("%.2f%%", av.Percent))+
				shell.Muted(fmt.Sprintf("  target ≥%.1f%%", metrics.UptimeTarget)), w))
	}

	fmt.Println(shell.KV("RESTARTS", shell.Value(fmt.Sprint(av.Restarts))+
		shell.Muted("  uptime counter reset"), w))
	if av.LongestGap > 0 {
		fmt.Println(shell.KV("LONGEST GAP",
			shell.Value(av.LongestGap.Round(time.Second).String())+
				shell.Muted(fmt.Sprintf("  heartbeat is every %s",
					metrics.UptimeInterval)), w))
	}
	fmt.Println(shell.PanelEnd())
}

// printAmbientPanel reports ambient category counts when any exist.
func printAmbientPanel(recs []metrics.Record) {
	counts := metrics.CategoryCounts(recs)
	if len(counts) == 0 {
		return
	}
	fmt.Println(shell.PanelTitle("ambient events"))
	cells := make([][]string, 0, len(counts))
	for _, cat := range []string{"loud_noise", "alarm_like", "music_like", "silence"} {
		if n, ok := counts[cat]; ok {
			cells = append(cells, []string{shell.Value(cat), shell.Muted(fmt.Sprint(n))})
		}
	}
	// Anything the analyzer grows later still gets shown rather than dropped.
	for cat, n := range counts {
		switch cat {
		case "loud_noise", "alarm_like", "music_like", "silence":
		default:
			cells = append(cells, []string{shell.Value(cat), shell.Muted(fmt.Sprint(n))})
		}
	}
	for _, l := range shell.Table([]string{"category", "events"}, cells) {
		fmt.Println(l)
	}
	fmt.Println(shell.PanelEnd())
}
