package main

import (
	"fmt"
	"image"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// texture is deterministic value noise at a few octaves: terrain-like
// detail at every scale, so shifts and blur behave as on real footage.
func texture(w, h int, seed int64) *image.Gray {
	rng := rand.New(rand.NewSource(seed))
	img := image.NewGray(image.Rect(0, 0, w, h))
	acc := make([]float64, w*h)
	for _, cell := range []int{97, 41, 17, 7, 3} {
		gw, gh := w/cell+2, h/cell+2
		grid := make([]float64, gw*gh)
		for i := range grid {
			grid[i] = rng.Float64()
		}
		for y := 0; y < h; y++ {
			fy := float64(y) / float64(cell)
			y0 := int(fy)
			ty := fy - float64(y0)
			for x := 0; x < w; x++ {
				fx := float64(x) / float64(cell)
				x0 := int(fx)
				tx := fx - float64(x0)
				a := grid[y0*gw+x0]*(1-tx) + grid[y0*gw+x0+1]*tx
				b := grid[(y0+1)*gw+x0]*(1-tx) + grid[(y0+1)*gw+x0+1]*tx
				acc[y*w+x] += (a*(1-ty) + b*ty) * float64(cell)
			}
		}
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range acc {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	for i, v := range acc {
		img.Pix[i] = uint8(20 + 215*(v-lo)/(hi-lo))
	}
	return img
}

func crop(src *image.Gray, x, y, w, h int) *image.Gray {
	return src.SubImage(image.Rect(x, y, x+w, y+h)).(*image.Gray)
}

func boxBlur(src *image.Gray, r int) *image.Gray {
	b := src.Bounds()
	out := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			sum, n := 0, 0
			for dy := -r; dy <= r; dy++ {
				for dx := -r; dx <= r; dx++ {
					xx, yy := x+dx, y+dy
					if xx >= 0 && yy >= 0 && xx < b.Dx() && yy < b.Dy() {
						sum += int(src.GrayAt(b.Min.X+xx, b.Min.Y+yy).Y)
						n++
					}
				}
			}
			out.Pix[y*out.Stride+x] = uint8(sum / n)
		}
	}
	return out
}

func uniform(w, h int, v uint8) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = v
	}
	return img
}

func mustMeasure(t *testing.T, img image.Image) Metrics {
	t.Helper()
	m, err := measure(img)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func mustView(t *testing.T, m Metrics) view {
	t.Helper()
	v, err := decodeView(m.Thumb)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMeasure(t *testing.T) {
	land := texture(1280, 720, 1)
	sharp := mustMeasure(t, land)
	blurred := mustMeasure(t, boxBlur(land, 3))
	if blurred.Sharpness > 0.5*sharp.Sharpness {
		t.Errorf("blur should halve sharpness: sharp %.1f blurred %.1f", sharp.Sharpness, blurred.Sharpness)
	}
	if sharp.Flat > 0.2 {
		t.Errorf("texture counted as flat: %.2f", sharp.Flat)
	}
	sky := mustMeasure(t, uniform(1280, 720, 180))
	if sky.Flat < 0.99 || sky.Sharpness > 1 {
		t.Errorf("uniform frame: flat %.2f sharpness %.2f", sky.Flat, sky.Sharpness)
	}
	black := mustMeasure(t, uniform(640, 360, 2))
	if black.Brightness > 3 || black.Clipped < 0.99 {
		t.Errorf("black frame: brightness %.1f clipped %.2f", black.Brightness, black.Clipped)
	}

	// The same view at 4K and 720p must score alike: that is what measuring
	// at workHeight is for.
	big := texture(2560, 1440, 2)
	small := image.NewGray(image.Rect(0, 0, 1280, 720))
	for y := 0; y < 720; y++ {
		for x := 0; x < 1280; x++ {
			s := int(big.Pix[2*y*big.Stride+2*x]) + int(big.Pix[2*y*big.Stride+2*x+1]) +
				int(big.Pix[(2*y+1)*big.Stride+2*x]) + int(big.Pix[(2*y+1)*big.Stride+2*x+1])
			small.Pix[y*small.Stride+x] = uint8(s / 4)
		}
	}
	a, b := mustMeasure(t, big), mustMeasure(t, small)
	if r := a.Sharpness / b.Sharpness; r < 0.8 || r > 1.25 {
		t.Errorf("resolution changed sharpness: 4K %.1f, 720p %.1f", a.Sharpness, b.Sharpness)
	}
}

func TestAlign(t *testing.T) {
	land := texture(1800, 1000, 3)
	base := mustView(t, mustMeasure(t, crop(land, 200, 100, 1280, 720)))
	for _, c := range []struct{ dx, dy int }{{0, 0}, {64, 0}, {-128, 36}, {256, -72}, {0, 144}} {
		v := mustView(t, mustMeasure(t, crop(land, 200+c.dx, 100+c.dy, 1280, 720)))
		m := align(base, v)
		// A crop further right is a camera moved right. One fine thumbnail
		// pixel is 20 source px, the tolerance.
		wantX, wantY := float64(c.dx)/1280, float64(c.dy)/720
		if math.Abs(m.DX-wantX) > 1.0/thumbW || math.Abs(m.DY-wantY) > 1.0/thumbH {
			t.Errorf("shift %v: got dx %.3f dy %.3f, want %.3f %.3f", c, m.DX, m.DY, wantX, wantY)
		}
		if m.Residual > 0.3 {
			t.Errorf("shift %v: residual %.2f after aligning the same picture", c, m.Residual)
		}
	}
	other := mustView(t, mustMeasure(t, crop(texture(1800, 1000, 4), 200, 100, 1280, 720)))
	if m := align(base, other); m.Residual < 0.6 {
		t.Errorf("unrelated pictures aligned with residual %.2f", m.Residual)
	}
}

func TestParseCuts(t *testing.T) {
	log := "frame=    0 fps=0.0 q=-0.0 size=       0kB time=00:00:00.00 bitrate=N/A speed=   0x    [scdet @ 0x5631] lavfi.scd.score: 34.896, lavfi.scd.time: 6\n" +
		"[scdet @ 0x5631] lavfi.scd.score: 42.475, lavfi.scd.time: 8.0333\n" +
		"[scdet @ 0x5631] lavfi.scd.score: 12.0, lavfi.scd.time: 8.0333\n" +
		"[scdet @ 0x5631] lavfi.scd.score: 17.230, lavfi.scd.time: 0\n"
	got := parseCuts(log)
	if fmt.Sprint(got) != "[6000 8033]" {
		t.Errorf("cuts %v", got)
	}
}

// flight builds a dataset in memory: frames of one source in order, each
// measured, with the manifest and analysis select reads.
type flight struct {
	t        *testing.T
	manifest Manifest
	analysis Analysis
}

func (f *flight) add(source string, img image.Image) {
	f.t.Helper()
	m := mustMeasure(f.t, img)
	n := 0
	for _, fr := range f.manifest.Frames {
		if fr.Source == source {
			n++
		}
	}
	b := img.Bounds()
	path := fmt.Sprintf("images/%s_%06d.jpg", source, n)
	f.manifest.Frames = append(f.manifest.Frames, FrameRecord{Path: path, Source: source, Category: "aerial",
		TimestampMS: int64(n) * 500, Frame: n, Width: b.Dx(), Height: b.Dy()})
	f.analysis.Frames = append(f.analysis.Frames, FrameAnalysis{Path: path, Metrics: m})
}

func (f *flight) source(name string, cuts []int64) {
	f.manifest.Sources = append(f.manifest.Sources, SourceRecord{Path: name, Category: "aerial",
		Width: 1280, Height: 720, CutsMS: cuts, CutsDetected: cuts != nil})
}

func decisions(sel Selection, source string) string {
	s := ""
	for _, d := range sel.Decisions {
		if d.Source != source {
			continue
		}
		mark := "K"
		if !d.Keep {
			mark = d.Reason[:3]
		}
		s += fmt.Sprintf("%d:%s ", d.Shot, mark)
	}
	return s
}

func TestSelectFlight(t *testing.T) {
	land := texture(2400, 1000, 5)
	f := &flight{t: t}
	// Shot 0: a pan, 64 px (5%) a sample, with one blurred sample in it.
	for i := 0; i < 12; i++ {
		img := crop(land, 40+64*i, 140, 1280, 720)
		if i == 5 {
			img = boxBlur(img, 4)
		}
		f.add("a", img)
	}
	// Then a hover: six identical samples of the pan's end.
	for i := 0; i < 6; i++ {
		f.add("a", crop(land, 40+64*11, 140, 1280, 720))
	}
	// Shot 1 (after a cut): a fade to black, then sky.
	f.add("a", uniform(1280, 720, 3))
	f.add("a", uniform(1280, 720, 3))
	f.add("a", uniform(1280, 720, 190))
	f.source("a", []int64{9000})

	sel := selectFrames(f.manifest, f.analysis, defaultSettings())
	got := decisions(sel, "a")
	t.Log(got)
	ds := sel.Decisions
	if ds[5].Reason != reasonBlur {
		t.Errorf("blurred pan frame: %q", ds[5].Reason)
	}
	if ds[18].Shot != 1 || ds[18].Reason != reasonDark || ds[19].Reason != reasonDark || ds[20].Reason != reasonFlat {
		t.Errorf("fade and sky after the cut: %s", got)
	}
	// 12 pan samples, 55% of a frame in all: a keyframe every ~20% gives 3-4.
	// The hover adds nothing: its frames repeat the pan's last view.
	if sel.Kept < 3 || sel.Kept > 5 {
		t.Errorf("kept %d keyframes, want 3-5: %s", sel.Kept, got)
	}
	hoverKept := 0
	for _, d := range ds[12:18] {
		if d.Keep {
			hoverKept++
		}
	}
	if hoverKept > 1 {
		t.Errorf("hover kept %d frames", hoverKept)
	}
	for _, d := range ds {
		if d.Reason == reasonSimilar && d.Ref == "" {
			t.Errorf("%s similar without a reference", d.Path)
		}
		if d.Warn != "" {
			t.Errorf("%s: unexpected warning %q", d.Path, d.Warn)
		}
	}
	if sel.Sources[0].Shots != 2 {
		t.Errorf("shots %d", sel.Sources[0].Shots)
	}
}

func TestSelectGapsAndDuplicates(t *testing.T) {
	land := texture(3000, 1000, 6)
	f := &flight{t: t}
	// Source a jumps 40% between samples: too little overlap.
	for i := 0; i < 4; i++ {
		f.add("a", crop(land, 40+512*i, 140, 1280, 720))
	}
	f.source("a", []int64{})
	// Source b re-films a's first view, brighter.
	view := crop(land, 40, 140, 1280, 720)
	brighter := image.NewGray(image.Rect(0, 0, 1280, 720))
	for y := 0; y < 720; y++ {
		for x := 0; x < 1280; x++ {
			brighter.Pix[y*brighter.Stride+x] = uint8(min(255, int(view.GrayAt(40+x, 140+y).Y)+25))
		}
	}
	f.add("b", brighter)
	f.add("b", crop(texture(1600, 900, 7), 0, 0, 1280, 720))
	f.source("b", []int64{})

	sel := selectFrames(f.manifest, f.analysis, defaultSettings())
	t.Log(decisions(sel, "a"), "|", decisions(sel, "b"))
	if sel.Sources[0].Gaps != 3 {
		t.Errorf("gaps in a: %d, want 3", sel.Sources[0].Gaps)
	}
	// Equal pixels and similar sharpness: whichever of the pair wins, exactly
	// one of a's first frame and b's re-film is a duplicate of the other.
	a0, b0 := sel.Decisions[0], sel.Decisions[4]
	if (a0.Reason == reasonDuplicate) == (b0.Reason == reasonDuplicate) {
		t.Errorf("re-filmed view: a0 %q, b0 %q", a0.Reason, b0.Reason)
	}
	if sel.Decisions[5].Reason != "" {
		t.Errorf("b's unrelated view dropped: %q", sel.Decisions[5].Reason)
	}
}

func TestFallbackCuts(t *testing.T) {
	f := &flight{t: t}
	land := texture(1800, 900, 8)
	for i := 0; i < 3; i++ {
		f.add("a", crop(land, 40*i, 0, 1280, 720))
	}
	dark := texture(1800, 900, 9)
	for i := range dark.Pix {
		dark.Pix[i] /= 3
	}
	for i := 0; i < 3; i++ {
		f.add("a", crop(dark, 40*i, 0, 1280, 720))
	}
	f.source("a", nil) // no scdet: old manifest
	sel := selectFrames(f.manifest, f.analysis, defaultSettings())
	if sel.Sources[0].CutsFrom != "frames" || sel.Sources[0].Shots != 2 || sel.Decisions[3].Shot != 1 {
		t.Errorf("fallback cuts: %s", decisions(sel, "a"))
	}
}

func TestSelectReturnPass(t *testing.T) {
	land := texture(2600, 1000, 10)
	f := &flight{t: t}
	// One shot, out and back over the same ground, 64 px a sample.
	for i := 0; i <= 18; i++ {
		f.add("a", crop(land, 40+64*i, 140, 1280, 720))
	}
	for i := 17; i >= 0; i-- {
		f.add("a", crop(land, 40+64*i, 140, 1280, 720))
	}
	f.source("a", []int64{})
	sel := selectFrames(f.manifest, f.analysis, defaultSettings())
	got := decisions(sel, "a")
	t.Log(got)
	out, back := 0, 0
	for i, d := range sel.Decisions {
		if d.Keep && i <= 18 {
			out++
		} else if d.Keep {
			back++
		}
	}
	// The way back covers the same ground: at most the keyframes next to
	// the turn (exempt as neighbours) survive.
	if out < 4 || back > 2 {
		t.Errorf("out %d kept, back %d kept: %s", out, back, got)
	}
}

func TestSnapCuts(t *testing.T) {
	a, b := texture(1800, 900, 11), texture(1800, 900, 12)
	f := &flight{t: t}
	for i := 0; i < 4; i++ {
		f.add("a", crop(a, 30*i, 0, 1280, 720))
	}
	for i := 0; i < 4; i++ {
		f.add("a", crop(b, 30*i, 0, 1280, 720))
	}
	// scdet's time lands a little after the nominal time of the first new
	// frame (2000 ms), so that frame starts out in the old shot.
	f.source("a", []int64{2070})
	sel := selectFrames(f.manifest, f.analysis, defaultSettings())
	if got := sel.Decisions[4].Shot; got != 1 {
		t.Errorf("first frame of the new view in shot %d: %s", got, decisions(sel, "a"))
	}
	if got := sel.Decisions[3].Shot; got != 0 {
		t.Errorf("last frame of the old view in shot %d", got)
	}
}

func TestBlurIsLocal(t *testing.T) {
	// One shot: ten samples of crisp detail, then ten of soft, hazy content
	// (as when the camera turns from the truss to distant slopes). The soft
	// stretch is content, not motion blur, and must not be rejected.
	crisp, soft := texture(2000, 900, 13), boxBlur(texture(2000, 900, 14), 3)
	f := &flight{t: t}
	for i := 0; i < 10; i++ {
		f.add("a", crop(crisp, 40*i, 0, 1280, 720))
	}
	for i := 0; i < 10; i++ {
		f.add("a", crop(soft, 40*i, 0, 1280, 720))
	}
	f.source("a", []int64{})
	sel := selectFrames(f.manifest, f.analysis, defaultSettings())
	if sel.Reasons[reasonBlur] > 0 {
		t.Errorf("soft stretch rejected as blur: %s", decisions(sel, "a"))
	}
}

func TestReportMissing(t *testing.T) {
	dir := t.TempDir()
	input, images := filepath.Join(dir, "videos"), filepath.Join(dir, "images")
	for _, d := range []string{filepath.Join(input, "aerial"), images} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// clip.mp4 was extracted from the input root, then moved into aerial/.
	oldFrame := filepath.Join(images, sourceID("clip.mp4")+"_000000.jpg")
	keepFrame := filepath.Join(images, sourceID("aerial/clip.mp4")+"_000000.jpg")
	for _, f := range []string{oldFrame, keepFrame} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := Manifest{
		Sources: []SourceRecord{{Path: "clip.mp4"}, {Path: "aerial/clip.mp4"}},
		Frames:  []FrameRecord{{Path: "images/a.jpg", Source: "clip.mp4"}, {Path: "images/b.jpg", Source: "aerial/clip.mp4"}},
	}
	videos := []string{filepath.Join(input, "aerial", "clip.mp4")}

	if err := reportMissing(&m, input, videos, images, false); err != nil {
		t.Fatal(err)
	}
	if len(m.Sources) != 2 {
		t.Fatalf("without --prune the manifest must not change: %v", m.Sources)
	}
	if err := reportMissing(&m, input, videos, images, true); err != nil {
		t.Fatal(err)
	}
	if len(m.Sources) != 1 || m.Sources[0].Path != "aerial/clip.mp4" || len(m.Frames) != 1 {
		t.Errorf("after prune: %v %v", m.Sources, m.Frames)
	}
	if _, err := os.Stat(oldFrame); !os.IsNotExist(err) {
		t.Errorf("old frame kept: %v", err)
	}
	if _, err := os.Stat(keepFrame); err != nil {
		t.Errorf("other source's frame removed: %v", err)
	}
}
