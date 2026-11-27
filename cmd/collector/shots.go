package main

import "sort"

// Shots: the frames of one source, split at cuts. Frames of different
// shots are never neighbours for keyframes, gaps or blur.

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
