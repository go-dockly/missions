package main

import (
	"encoding/base64"
	"errors"
	"math"
)

// Thumbnails are stretched to 16:9 whatever the source aspect: they are only
// compared with each other, and a fixed size keeps alignment simple.
const (
	thumbW = 64
	thumbH = 36
)

func encodeThumb(g gray) string {
	raw := make([]byte, len(g.px))
	for i, p := range g.px {
		raw[i] = byte(math.Max(0, math.Min(255, math.Round(float64(p)))))
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// plane is a thumbnail normalised to zero mean and unit deviation, so a
// brighter or flatter copy of a view still compares as the same view.
type plane struct {
	w, h int
	px   []float32
}

// view holds one thumbnail at three sizes: coarse for the wide shift search,
// fine to refine it, tiny for the cheap duplicate pre-check.
type view struct {
	fine, coarse, tiny plane
}

func decodeView(s string) (view, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return view{}, err
	}
	if len(raw) != thumbW*thumbH {
		return view{}, errors.New("thumbnail has the wrong size (re-run analyze --force)")
	}
	g := gray{w: thumbW, h: thumbH, px: make([]float32, len(raw))}
	for i, b := range raw {
		g.px[i] = float32(b)
	}
	return view{
		fine:   normalise(g),
		coarse: normalise(resize(g, thumbW/2, thumbH/2)),
		tiny:   normalise(resize(g, thumbW/4, thumbH/4)),
	}, nil
}

func normalise(g gray) plane {
	var sum, sum2 float64
	for _, p := range g.px {
		sum += float64(p)
		sum2 += float64(p) * float64(p)
	}
	n := float64(len(g.px))
	mean := sum / n
	std := math.Sqrt(math.Max(0, sum2/n-mean*mean))
	// A flat frame has nothing to normalise; leave it flat rather than
	// blowing compression noise up into texture.
	std = math.Max(std, 4)
	p := plane{w: g.w, h: g.h, px: make([]float32, len(g.px))}
	for i, v := range g.px {
		p.px[i] = float32((float64(v) - mean) / std)
	}
	return p
}

// Motion is how one view relates to another after the best global shift.
type Motion struct {
	// Camera motion as a share of the frame: positive DX, the camera moved
	// right (the picture slid left); positive DY, it moved down.
	DX, DY float64
	// Shift is 1 - overlap of the two frames at that shift.
	Shift float64
	// Residual is the mean difference left after aligning, in units of the
	// thumbnail's deviation: ~0 the same view, ~1.1 unrelated pictures.
	// Forward flight, zoom, turning and parallax show up here, not in Shift.
	Residual float64
}

// minOverlap bounds the search: below this much overlap a match is too
// small to trust, and the frames count as unrelated anyway.
const minOverlap = 0.45

func align(a, b view) Motion {
	rx, ry := int(0.35*float64(a.coarse.w)), int(0.35*float64(a.coarse.h))
	best, bx, by := math.Inf(1), 0, 0
	for sy := -ry; sy <= ry; sy++ {
		for sx := -rx; sx <= rx; sx++ {
			if d, ok := mad(a.coarse, b.coarse, sx, sy); ok && d < best {
				best, bx, by = d, sx, sy
			}
		}
	}
	best = math.Inf(1)
	fx, fy := 2*bx, 2*by
	for sy := 2*by - 2; sy <= 2*by+2; sy++ {
		for sx := 2*bx - 2; sx <= 2*bx+2; sx++ {
			if d, ok := mad(a.fine, b.fine, sx, sy); ok && d < best {
				best, fx, fy = d, sx, sy
			}
		}
	}
	if math.IsInf(best, 1) {
		return Motion{Shift: 1, Residual: 2}
	}
	dx, dy := float64(fx)/float64(a.fine.w), float64(fy)/float64(a.fine.h)
	return Motion{DX: dx, DY: dy, Shift: 1 - (1-math.Abs(dx))*(1-math.Abs(dy)), Residual: best}
}

// quickResidual is a cheap lower-bound-ish check on 16×9 thumbnails with
// small shifts, to skip full alignment of pairs that are clearly different.
func quickResidual(a, b view) float64 {
	best := math.Inf(1)
	for sy := -1; sy <= 1; sy++ {
		for sx := -2; sx <= 2; sx++ {
			if d, ok := mad(a.tiny, b.tiny, sx, sy); ok && d < best {
				best = d
			}
		}
	}
	return best
}

// mad is the mean absolute difference where a(x, y) meets b(x-sx, y-sy).
func mad(a, b plane, sx, sy int) (float64, bool) {
	x0, x1 := max(0, sx), min(a.w, a.w+sx)
	y0, y1 := max(0, sy), min(a.h, a.h+sy)
	if (x1-x0)*(y1-y0) < int(minOverlap*float64(a.w*a.h)) {
		return 0, false
	}
	var sum float32
	for y := y0; y < y1; y++ {
		ra := a.px[y*a.w : (y+1)*a.w]
		rb := b.px[(y-sy)*b.w : (y-sy+1)*b.w]
		for x := x0; x < x1; x++ {
			d := ra[x] - rb[x-sx]
			if d < 0 {
				d = -d
			}
			sum += d
		}
	}
	return float64(sum) / float64((x1-x0)*(y1-y0)), true
}
