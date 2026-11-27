package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Settings are select's thresholds. Defaults are starting points to tune by
// eye on sheet.html, not measured optima.
type Settings struct {
	Dark         float64 `json:"dark"`          // mean luma below: a fade (dim footage sits well above)
	Bright       float64 `json:"bright"`        // mean luma above: blown out or white card
	Clipped      float64 `json:"clipped"`       // share of crushed/blown pixels above
	Flat         float64 `json:"flat"`          // share of textureless blocks above: sky, haze, title card
	BlurRel      float64 `json:"blur_relative"` // sharpness below this share of the median of nearby frames
	MinSharpness float64 `json:"min_sharpness"` // absolute floor (0: off); rejects whole soft sources
	// A keyframe window ends when the view has moved MinShift (1 - overlap)
	// or changed by MinResidual after aligning (forward flight, turns).
	MinShift    float64 `json:"min_shift"`
	MinResidual float64 `json:"min_residual"`
	// Consecutive samples further apart than this lose overlap: warn.
	GapShift    float64 `json:"gap_shift"`
	GapResidual float64 `json:"gap_residual"`
	// Frames of different shots or sources aligning below this residual
	// show the same view (0: off).
	Dup float64 `json:"duplicate"`
	// Fallback cuts (no scdet): tones jump (histogram L1 above CutHist) and
	// the views don't align (residual above CutResidual).
	CutHist     float64 `json:"cut_histogram"`
	CutResidual float64 `json:"cut_residual"`
}

// blurRadius is how many samples either side a frame's sharpness is
// compared with (±2 s at the default 500 ms).
const blurRadius = 4

func defaultSettings() Settings {
	return Settings{
		Dark: 12, Bright: 240, Clipped: 0.4, Flat: 0.85,
		BlurRel: 0.5, MinSharpness: 0,
		MinShift: 0.2, MinResidual: 0.4,
		GapShift: 0.35, GapResidual: 0.6, Dup: 0.25,
		CutHist: 0.5, CutResidual: 0.8,
	}
}

type Decision struct {
	Path        string  `json:"path"`
	Source      string  `json:"source"`
	Category    string  `json:"category"`
	Frame       int     `json:"frame"`
	TimestampMS int64   `json:"timestamp_ms"`
	Shot        int     `json:"shot"`
	Keep        bool    `json:"keep"`
	Reason      string  `json:"reason,omitempty"` // why it was dropped
	Ref         string  `json:"ref,omitempty"`    // the kept frame that stands in for it
	Warn        string  `json:"warn,omitempty"`
	Sharpness   float64 `json:"sharpness"`
	// RelSharpness is sharpness over the median of nearby frames (the blur test).
	RelSharpness float64 `json:"relative_sharpness"`

	metrics Metrics
	view    view
	pixels  int
	key     int // index among the shot's keyframes, for removeDuplicates
}

// Reasons, in the order rules run. The sheet colours by these.
const (
	reasonMissing   = "missing"
	reasonDark      = "dark"
	reasonBright    = "bright"
	reasonExposure  = "exposure"
	reasonFlat      = "flat"
	reasonBlur      = "blur"
	reasonSimilar   = "similar"
	reasonDuplicate = "duplicate"
)

var reasonOrder = []string{reasonMissing, reasonDark, reasonBright, reasonExposure, reasonFlat, reasonBlur, reasonSimilar, reasonDuplicate}

type SourceSummary struct {
	Path       string         `json:"path"`
	Category   string         `json:"category"`
	Resolution string         `json:"resolution"`
	CutsFrom   string         `json:"cuts_from"` // "scdet" or "frames"
	Frames     int            `json:"frames"`
	Shots      int            `json:"shots"`
	Kept       int            `json:"kept"`
	Gaps       int            `json:"gaps"` // samples that jumped too far from the previous one
	Reasons    map[string]int `json:"reasons"`
	// Sharpness percentiles of textured frames: compares sources' quality.
	SharpP10 float64 `json:"sharpness_p10"`
	SharpP50 float64 `json:"sharpness_p50"`
	SharpP90 float64 `json:"sharpness_p90"`
}

type Selection struct {
	Version     int             `json:"version"`
	GeneratedAt time.Time       `json:"generated_at"`
	Settings    Settings        `json:"settings"`
	Frames      int             `json:"frames"`
	Kept        int             `json:"kept"`
	Reasons     map[string]int  `json:"reasons"`
	Sources     []SourceSummary `json:"sources"`
	Decisions   []Decision      `json:"decisions"`
}

func runSelect(args []string) error {
	s := defaultSettings()
	flags := flag.NewFlagSet("select", flag.ContinueOnError)
	dataset := flags.String("dataset", "", "dataset directory (required)")
	sheet := flags.Bool("sheet", true, "write sheet.html, the contact sheet")
	flags.Float64Var(&s.Dark, "dark", s.Dark, "reject mean luma below (0-255)")
	flags.Float64Var(&s.Bright, "bright", s.Bright, "reject mean luma above (0-255)")
	flags.Float64Var(&s.Clipped, "clipped", s.Clipped, "reject share of crushed/blown pixels above")
	flags.Float64Var(&s.Flat, "flat", s.Flat, "reject share of textureless blocks above (sky, title cards)")
	flags.Float64Var(&s.BlurRel, "blur", s.BlurRel, "reject sharpness below this share of nearby frames' median")
	flags.Float64Var(&s.MinSharpness, "min-sharpness", s.MinSharpness, "reject sharpness below (0: off)")
	flags.Float64Var(&s.MinShift, "min-shift", s.MinShift, "new keyframe once the view moved this much (1 - overlap)")
	flags.Float64Var(&s.MinResidual, "min-residual", s.MinResidual, "...or changed this much after aligning (0 same, ~1.1 unrelated)")
	flags.Float64Var(&s.GapShift, "gap-shift", s.GapShift, "warn when consecutive samples moved more than this")
	flags.Float64Var(&s.GapResidual, "gap-residual", s.GapResidual, "...or changed more than this after aligning")
	flags.Float64Var(&s.Dup, "duplicate", s.Dup, "same view across shots below this aligned residual (0: off)")
	flags.Float64Var(&s.CutHist, "cut-histogram", s.CutHist, "fallback cuts: histogram distance above (0-2)")
	flags.Float64Var(&s.CutResidual, "cut-residual", s.CutResidual, "fallback cuts: aligned residual above")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dataset == "" {
		return errors.New("--dataset is required")
	}
	manifest, err := readManifest(filepath.Join(*dataset, "manifest.json"))
	if err != nil {
		return err
	}
	analysis, err := readAnalysis(filepath.Join(*dataset, "analysis.json"))
	if err != nil {
		return fmt.Errorf("no analysis (run analyze first): %w", err)
	}
	sel := selectFrames(manifest, analysis, s)
	sel.GeneratedAt = time.Now().UTC()

	if err := writeJSON(filepath.Join(*dataset, "selection.json"), sel); err != nil {
		return err
	}
	var list strings.Builder
	for _, d := range sel.Decisions {
		if d.Keep {
			// colmap's --image_list_path names are relative to --image_path.
			list.WriteString(strings.TrimPrefix(d.Path, "images/"))
			list.WriteByte('\n')
		}
	}
	if err := os.WriteFile(filepath.Join(*dataset, "selected.txt"), []byte(list.String()), 0o644); err != nil {
		return err
	}
	if *sheet {
		if err := writeSheet(filepath.Join(*dataset, "sheet.html"), sel); err != nil {
			return err
		}
	}
	printReport(os.Stdout, sel)
	fmt.Printf("\nSelection: %s\nCOLMAP list: %s\n", filepath.Join(*dataset, "selection.json"), filepath.Join(*dataset, "selected.txt"))
	if *sheet {
		fmt.Printf("Contact sheet: %s\n", filepath.Join(*dataset, "sheet.html"))
	}
	return nil
}

// selectFrames is the whole decision, pure so tests can drive it.
func selectFrames(manifest Manifest, analysis Analysis, s Settings) Selection {
	byPath := map[string]FrameAnalysis{}
	for _, a := range analysis.Frames {
		byPath[a.Path] = a
	}
	sources := map[string]SourceRecord{}
	for _, src := range manifest.Sources {
		sources[src.Path] = src
	}
	groups := map[string][]*Decision{}
	var order []string
	for _, fr := range manifest.Frames {
		d := &Decision{Path: fr.Path, Source: fr.Source, Category: fr.Category, Frame: fr.Frame,
			TimestampMS: fr.TimestampMS, pixels: fr.Width * fr.Height}
		if a, ok := byPath[fr.Path]; !ok || a.Error != "" {
			d.Reason = reasonMissing
		} else if v, err := decodeView(a.Thumb); err != nil {
			d.Reason = reasonMissing
		} else {
			d.metrics, d.view, d.Sharpness = a.Metrics, v, a.Sharpness
		}
		if _, seen := groups[fr.Source]; !seen {
			order = append(order, fr.Source)
		}
		groups[fr.Source] = append(groups[fr.Source], d)
	}
	sort.Strings(order)

	sel := Selection{Version: 1, Settings: s, Reasons: map[string]int{}}
	var all []*Decision
	for _, name := range order {
		frames := groups[name]
		sort.Slice(frames, func(i, j int) bool { return frames[i].Frame < frames[j].Frame })
		src := sources[name]
		sum := SourceSummary{Path: name, Category: src.Category, Frames: len(frames), Reasons: map[string]int{},
			Resolution: fmt.Sprintf("%dx%d", src.Width, src.Height)}
		if src.CutsDetected {
			sum.CutsFrom = "scdet"
			assignShotsByCuts(frames, src.CutsMS)
		} else {
			sum.CutsFrom = "frames"
			assignShotsByFrames(frames, s)
		}
		for start := 0; start < len(frames); {
			end := start
			for end < len(frames) && frames[end].Shot == frames[start].Shot {
				end++
			}
			sum.Gaps += selectShot(frames[start:end], s)
			start = end
		}
		if n := len(frames); n > 0 {
			sum.Shots = frames[n-1].Shot + 1
		}
		var sharp []float64
		for _, d := range frames {
			if d.Reason != reasonMissing && d.Reason != reasonDark && d.Reason != reasonFlat {
				sharp = append(sharp, d.Sharpness)
			}
		}
		sum.SharpP10, sum.SharpP50, sum.SharpP90 = percentile(sharp, 0.1), percentile(sharp, 0.5), percentile(sharp, 0.9)
		sel.Sources = append(sel.Sources, sum)
		all = append(all, frames...)
	}

	if s.Dup > 0 {
		removeDuplicates(all, s.Dup, s.MinShift)
	}

	sort.Slice(all, func(i, j int) bool { return all[i].Path < all[j].Path })
	index := map[string]int{}
	for i, src := range sel.Sources {
		index[src.Path] = i
	}
	for _, d := range all {
		d.Keep = d.Reason == ""
		sum := &sel.Sources[index[d.Source]]
		if d.Keep {
			sel.Kept++
			sum.Kept++
		} else {
			sel.Reasons[d.Reason]++
			sum.Reasons[d.Reason]++
		}
		sel.Decisions = append(sel.Decisions, *d)
	}
	sel.Frames = len(all)
	return sel
}

func assignShotsByCuts(frames []*Decision, cuts []int64) {
	for _, d := range frames {
		d.Shot = sort.Search(len(cuts), func(i int) bool { return cuts[i] > d.TimestampMS })
	}
	renumber(frames)
	snapCuts(frames)
}

// snapCuts moves a frame next to a cut to the side it belongs to. The
// manifest's timestamps are nominal (index × interval) while scdet's are
// the source's, so the sample at a cut can land on either side of it.
func snapCuts(frames []*Decision) {
	usable := func(i int) bool { return i >= 0 && i < len(frames) && frames[i].Reason != reasonMissing }
	residual := func(i, j int) float64 { return align(frames[i].view, frames[j].view).Residual }
	for i := 0; i+1 < len(frames); i++ {
		p, n := i, i+1
		if frames[p].Shot == frames[n].Shot || !usable(p) || !usable(n) {
			continue
		}
		across := residual(p, n)
		switch {
		case usable(p-1) && frames[p-1].Shot == frames[p].Shot && across < residual(p-1, p):
			frames[p].Shot = frames[n].Shot // p already shows the next shot
		case usable(n+1) && frames[n+1].Shot == frames[n].Shot && across < residual(n, n+1):
			frames[n].Shot = frames[p].Shot // n still shows the previous one
		}
	}
	renumber(frames)
}

// assignShotsByFrames is the fallback for manifests without scdet cuts. At
// 1-2 samples a second a fast pan also changes a lot, so a cut needs both
// the tones (histogram) to jump and the views to stop aligning.
func assignShotsByFrames(frames []*Decision, s Settings) {
	shot := 0
	var prev *Decision
	for _, d := range frames {
		if d.Reason == reasonMissing {
			d.Shot = shot
			continue
		}
		if prev != nil && histDistance(prev.metrics.Histogram, d.metrics.Histogram) > s.CutHist &&
			align(prev.view, d.view).Residual > s.CutResidual {
			shot++
		}
		d.Shot = shot
		prev = d
	}
}

// renumber makes shot numbers dense: a cut between two samples, or several
// cuts inside one sample interval, must not leave empty shots.
func renumber(frames []*Decision) {
	next, last := -1, -1
	for _, d := range frames {
		if d.Shot != last {
			last = d.Shot
			next++
		}
		d.Shot = next
	}
}

// selectShot applies the quality rules, then keeps the sharpest frame of each
// window of nearly-identical views. Returns the number of gap warnings.
func selectShot(frames []*Decision, s Settings) int {
	for _, d := range frames {
		if d.Reason != "" {
			continue
		}
		m := d.metrics
		switch {
		case m.Brightness < s.Dark:
			d.Reason = reasonDark
		case m.Brightness > s.Bright:
			d.Reason = reasonBright
		case m.Clipped > s.Clipped:
			d.Reason = reasonExposure
		case m.Flat > s.Flat:
			d.Reason = reasonFlat
		}
	}
	// Blur is judged against the frame's neighbours, not the whole shot:
	// motion blur lasts a few samples, while what the camera looks at (sharp
	// truss, hazy distant slopes) changes sharpness for longer than that.
	var candidates []*Decision
	for i, d := range frames {
		var near []float64
		for _, o := range frames[max(0, i-blurRadius):min(len(frames), i+blurRadius+1)] {
			if o.Reason == "" || o.Reason == reasonBlur {
				near = append(near, o.Sharpness)
			}
		}
		median := percentile(near, 0.5)
		if median > 0 {
			d.RelSharpness = d.Sharpness / median
		}
		if d.Reason != "" {
			continue
		}
		if d.Sharpness < s.BlurRel*median || (s.MinSharpness > 0 && d.Sharpness < s.MinSharpness) {
			d.Reason = reasonBlur
			continue
		}
		candidates = append(candidates, d)
	}

	// Gaps: samples too far apart for COLMAP to match neighbours. Blurry
	// frames count as samples here; they bridge nothing, but are not gaps
	// in the footage either.
	low := 0
	var prev *Decision
	for _, d := range frames {
		if d.Reason != "" && d.Reason != reasonBlur {
			continue
		}
		if prev != nil {
			if m := align(prev.view, d.view); m.Shift > s.GapShift || m.Residual > s.GapResidual {
				d.Warn = fmt.Sprintf("jump from previous sample (moved %.2f, residual %.2f): extract denser", m.Shift, m.Residual)
				low++
			}
		}
		prev = d
	}

	keys := 0
	// A window runs from its first frame while the view stays close to it.
	// Measuring from the window start, not the previous frame, stops a slow
	// drift from chaining into one endless window.
	for i := 0; i < len(candidates); {
		j := i + 1
		for j < len(candidates) {
			m := align(candidates[i].view, candidates[j].view)
			if m.Shift >= s.MinShift || m.Residual >= s.MinResidual {
				break
			}
			j++
		}
		best := candidates[i]
		for _, d := range candidates[i:j] {
			if d.Sharpness > best.Sharpness {
				best = d
			}
		}
		for _, d := range candidates[i:j] {
			if d != best {
				d.Reason, d.Ref = reasonSimilar, best.Path
			}
		}
		best.key = keys
		keys++
		i = j
	}
	return low
}

// removeDuplicates drops kept frames that show the same view as a better
// kept frame: re-uploads, a second lap around the bridge, the same famous
// angle filmed by twenty pilots. Better means more pixels, then sharper.
// The same view means overlapping more than keyframes do (shift below
// minShift) and aligning with residual below limit. A shot's neighbouring
// keyframes are exempt: they overlap by design.
func removeDuplicates(all []*Decision, limit, minShift float64) {
	var kept []*Decision
	for _, d := range all {
		if d.Reason == "" {
			kept = append(kept, d)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].pixels != kept[j].pixels {
			return kept[i].pixels > kept[j].pixels
		}
		return kept[i].Sharpness > kept[j].Sharpness
	})
	var accepted []*Decision
	for _, d := range kept {
		dup := false
		for _, a := range accepted {
			if a.Source == d.Source && a.Shot == d.Shot && abs(a.key-d.key) <= 2 {
				continue
			}
			if quickResidual(a.view, d.view) > 2*limit {
				continue
			}
			if m := align(a.view, d.view); m.Residual < limit && m.Shift < minShift {
				d.Reason, d.Ref = reasonDuplicate, a.Path
				dup = true
				break
			}
		}
		if !dup {
			accepted = append(accepted, d)
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	v := append([]float64(nil), values...)
	sort.Float64s(v)
	i := int(p*float64(len(v)-1) + 0.5)
	return v[i]
}

func printReport(w *os.File, sel Selection) {
	fmt.Fprintf(w, "%d frame(s), %d kept (%.0f%%)\n", sel.Frames, sel.Kept, pct(sel.Kept, sel.Frames))
	fmt.Fprintf(w, "rejected: %s\n", reasonsText(sel.Reasons))
	for _, s := range sel.Sources {
		fmt.Fprintf(w, "\n%s [%s, %s]\n", s.Path, s.Category, s.Resolution)
		fmt.Fprintf(w, "  %d frame(s), %d shot(s) (cuts from %s), %d kept\n", s.Frames, s.Shots, s.CutsFrom, s.Kept)
		fmt.Fprintf(w, "  rejected: %s\n", reasonsText(s.Reasons))
		fmt.Fprintf(w, "  sharpness p10/p50/p90: %.0f / %.0f / %.0f\n", s.SharpP10, s.SharpP50, s.SharpP90)
		if s.Gaps > 0 {
			fmt.Fprintf(w, "  %d jump(s) between samples: extract this source denser\n", s.Gaps)
		}
	}
}

func reasonsText(r map[string]int) string {
	var parts []string
	for _, k := range reasonOrder {
		if r[k] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", k, r[k]))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}
