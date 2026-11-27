package main

import (
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
)

// Frames are measured at a common height so scores compare across sources:
// a 4K original and a 720p re-upload of the same view should get similar
// sharpness, which full-resolution Laplacians would not give.
const workHeight = 360

// Metrics are the per-frame numbers select decides on. All are cheap,
// classical measures; nothing here needs a model.
type Metrics struct {
	Sharpness  float64     `json:"sharpness"`  // variance of the Laplacian at workHeight
	Brightness float64     `json:"brightness"` // mean luma, 0-255
	Contrast   float64     `json:"contrast"`   // luma standard deviation
	Clipped    float64     `json:"clipped"`    // share of pixels crushed (<8) or blown (>247)
	Flat       float64     `json:"flat"`       // share of 8x8 blocks with almost no texture (sky, title cards, fades)
	Thumb      string      `json:"thumb"`      // thumbW×thumbH luma, base64; select aligns these (motion.go)
	Histogram  [16]float64 `json:"histogram"`  // luma histogram, sums to 1
}

type gray struct {
	w, h int
	px   []float32 // 0-255
}

func measureFile(path string) (Metrics, error) {
	f, err := os.Open(path)
	if err != nil {
		return Metrics{}, err
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		return Metrics{}, err
	}
	return measure(img)
}

func measure(img image.Image) (Metrics, error) {
	g := toGray(img)
	if g.w < 16 || g.h < 16 {
		return Metrics{}, errors.New("image too small")
	}
	h := workHeight
	if g.h < h {
		h = g.h
	}
	w := int(math.Round(float64(g.w) * float64(h) / float64(g.h)))
	small := resize(g, w, h)

	var m Metrics
	m.Sharpness = laplacianVariance(small)
	m.Brightness, m.Contrast, m.Clipped, m.Histogram = tone(small)
	m.Flat = flatShare(small, 8, 3)
	m.Thumb = encodeThumb(resize(small, thumbW, thumbH))
	return m, nil
}

// toGray reads the luma plane directly when the decoder gives YCbCr (every
// baseline JPEG), which is most of the time spent otherwise.
func toGray(img image.Image) gray {
	b := img.Bounds()
	g := gray{w: b.Dx(), h: b.Dy(), px: make([]float32, b.Dx()*b.Dy())}
	switch src := img.(type) {
	case *image.YCbCr:
		for y := 0; y < g.h; y++ {
			row := src.Y[src.YOffset(b.Min.X, b.Min.Y+y):]
			for x := 0; x < g.w; x++ {
				g.px[y*g.w+x] = float32(row[x])
			}
		}
	case *image.Gray:
		for y := 0; y < g.h; y++ {
			row := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]
			for x := 0; x < g.w; x++ {
				g.px[y*g.w+x] = float32(row[x])
			}
		}
	default:
		for y := 0; y < g.h; y++ {
			for x := 0; x < g.w; x++ {
				c := color.GrayModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.Gray)
				g.px[y*g.w+x] = float32(c.Y)
			}
		}
	}
	return g
}

// resize is an area average: each source pixel lands in exactly one target
// pixel. Good enough for shrinking, which is all it is used for.
func resize(g gray, w, h int) gray {
	if w == g.w && h == g.h {
		return g
	}
	out := gray{w: w, h: h, px: make([]float32, w*h)}
	count := make([]float32, w*h)
	for y := 0; y < g.h; y++ {
		ty := y * h / g.h
		for x := 0; x < g.w; x++ {
			i := ty*w + x*w/g.w
			out.px[i] += g.px[y*g.w+x]
			count[i]++
		}
	}
	for i := range out.px {
		if count[i] > 0 {
			out.px[i] /= count[i]
		}
	}
	return out
}

func laplacianVariance(g gray) float64 {
	var sum, sum2 float64
	n := 0
	for y := 1; y < g.h-1; y++ {
		for x := 1; x < g.w-1; x++ {
			i := y*g.w + x
			l := float64(g.px[i-1] + g.px[i+1] + g.px[i-g.w] + g.px[i+g.w] - 4*g.px[i])
			sum += l
			sum2 += l * l
			n++
		}
	}
	mean := sum / float64(n)
	return sum2/float64(n) - mean*mean
}

func tone(g gray) (mean, std, clipped float64, hist [16]float64) {
	var sum, sum2 float64
	clip := 0
	for _, p := range g.px {
		v := float64(p)
		sum += v
		sum2 += v * v
		if p < 8 || p > 247 {
			clip++
		}
		bin := int(p) / 16
		if bin > 15 {
			bin = 15
		}
		hist[bin]++
	}
	n := float64(len(g.px))
	mean = sum / n
	std = math.Sqrt(math.Max(0, sum2/n-mean*mean))
	for i := range hist {
		hist[i] /= n
	}
	return mean, std, float64(clip) / n, hist
}

// flatShare is the share of size×size blocks whose standard deviation is
// below limit. Sky, haze, fades and title-card backgrounds are flat; they
// give COLMAP nothing to match.
func flatShare(g gray, size int, limit float64) float64 {
	flat, total := 0, 0
	for by := 0; by+size <= g.h; by += size {
		for bx := 0; bx+size <= g.w; bx += size {
			var sum, sum2 float64
			for y := by; y < by+size; y++ {
				for x := bx; x < bx+size; x++ {
					v := float64(g.px[y*g.w+x])
					sum += v
					sum2 += v * v
				}
			}
			n := float64(size * size)
			mean := sum / n
			if math.Sqrt(math.Max(0, sum2/n-mean*mean)) < limit {
				flat++
			}
			total++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(flat) / float64(total)
}

// histDistance is the L1 distance of two histograms, 0-2.
func histDistance(a, b [16]float64) float64 {
	d := 0.0
	for i := range a {
		d += math.Abs(a[i] - b[i])
	}
	return d
}
