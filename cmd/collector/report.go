package main

import (
	"fmt"
	"io"
	"strings"
)

func printReport(w io.Writer, sel Selection) {
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
