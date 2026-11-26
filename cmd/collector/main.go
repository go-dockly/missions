package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type probeResult struct {
	Streams []struct {
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
		Duration     string `json:"duration"`
	} `json:"streams"`
	Format struct {
		Duration   string `json:"duration"`
		FormatName string `json:"format_name"`
		Size       string `json:"size"`
		BitRate    string `json:"bit_rate"`
	} `json:"format"`
}

type FrameRecord struct {
	Path        string `json:"path"`
	Source      string `json:"source"`
	Category    string `json:"category"`
	TimestampMS int64  `json:"timestamp_ms"`
	Frame       int    `json:"frame"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Codec       string `json:"codec"`
}

type SourceRecord struct {
	Path        string  `json:"path"`
	Category    string  `json:"category"`
	DurationSec float64 `json:"duration_seconds"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	Codec       string  `json:"codec"`
	FrameRate   string  `json:"frame_rate"`
	Frames      int     `json:"frames_extracted"`
}

type Manifest struct {
	Version     int            `json:"version"`
	GeneratedAt time.Time      `json:"generated_at"`
	Interval    string         `json:"interval"`
	Sources     []SourceRecord `json:"sources"`
	Frames      []FrameRecord  `json:"frames"`
}

var videoExtensions = map[string]bool{
	".mp4": true, ".mov": true, ".m4v": true, ".mkv": true,
	".avi": true, ".webm": true, ".mpg": true, ".mpeg": true,
	".mts": true, ".m2ts": true, ".3gp": true,
}

func main() {
	if len(os.Args) < 2 || os.Args[1] != "extract" {
		usage()
		os.Exit(2)
	}
	if err := runExtract(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`reference-collector — prepare local video references for COLMAP

Usage:
  reference-collector extract --input ./videos --output ./dataset --interval 1s

Options:
  --input             Directory containing local videos (required)
  --output            Dataset directory (required)
  --interval          Sampling interval, e.g. 1s, 500ms, 2s (default 1s)
  --category          Override inferred category for every input video
  --include-historical Include folders named "historical" (default false)
  --recursive         Search subdirectories (default true)
  --ffmpeg            FFmpeg executable (default ffmpeg)
  --ffprobe           FFprobe executable (default ffprobe)
  --quality           JPEG quality, 2 is high quality (1-31; default 2)

Output:
  <output>/images/       Flat image directory suitable for COLMAP
  <output>/manifest.json Source and per-frame provenance`)
}

func runExtract(args []string) error {
	flags := flag.NewFlagSet("extract", flag.ContinueOnError)
	input := flags.String("input", "", "input video directory")
	output := flags.String("output", "", "output dataset directory")
	interval := flags.Duration("interval", time.Second, "frame sampling interval")
	categoryOverride := flags.String("category", "", "category override")
	includeHistorical := flags.Bool("include-historical", false, "include historical footage")
	recursive := flags.Bool("recursive", true, "walk input recursively")
	ffmpeg := flags.String("ffmpeg", "ffmpeg", "ffmpeg executable")
	ffprobe := flags.String("ffprobe", "ffprobe", "ffprobe executable")
	quality := flags.Int("quality", 2, "JPEG quality (1 best, 31 worst)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *input == "" || *output == "" {
		return errors.New("--input and --output are required")
	}
	if *interval <= 0 {
		return errors.New("--interval must be greater than zero")
	}
	if *quality < 1 || *quality > 31 {
		return errors.New("--quality must be between 1 and 31")
	}
	if _, err := exec.LookPath(*ffmpeg); err != nil {
		return fmt.Errorf("cannot find FFmpeg executable %q: %w", *ffmpeg, err)
	}
	if _, err := exec.LookPath(*ffprobe); err != nil {
		return fmt.Errorf("cannot find FFprobe executable %q: %w", *ffprobe, err)
	}

	inputAbs, err := filepath.Abs(*input)
	if err != nil {
		return err
	}
	outputAbs, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	info, err := os.Stat(inputAbs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("input must be a directory: %s", inputAbs)
	}

	var videos []string
	err = filepath.WalkDir(inputAbs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != inputAbs && !*recursive {
				return filepath.SkipDir
			}
			if !*includeHistorical && strings.EqualFold(d.Name(), "historical") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if videoExtensions[strings.ToLower(filepath.Ext(path))] {
			videos = append(videos, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(videos)
	if len(videos) == 0 {
		return fmt.Errorf("no supported video files found under %s", inputAbs)
	}

	imageDir := filepath.Join(outputAbs, "images")
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		return err
	}

	manifestPath := filepath.Join(outputAbs, "manifest.json")
	manifest := loadManifest(manifestPath)
	manifest.Version = 1
	manifest.GeneratedAt = time.Now().UTC()
	manifest.Interval = interval.String()

	fps := 1.0 / interval.Seconds()
	fpsText := strconv.FormatFloat(fps, 'f', 8, 64)
	fmt.Printf("Found %d video(s); sampling at %s (%s fps)\n", len(videos), *interval, fpsText)

	success, failed := 0, 0
	for i, video := range videos {
		rel, err := filepath.Rel(inputAbs, video)
		if err != nil {
			return err
		}
		category := *categoryOverride
		if category == "" {
			category = inferCategory(rel)
		}
		id := sourceID(rel)
		prefix := id + "_"

		fmt.Printf("[%d/%d] %s [%s]\n", i+1, len(videos), rel, category)
		probe, err := probeVideo(*ffprobe, video)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  probe failed: %v\n", err)
			failed++
			continue
		}

		// Remove previous frames for this source so a shorter re-run cannot
		// leave stale images behind.
		oldMatches, _ := filepath.Glob(filepath.Join(imageDir, prefix+"*.jpg"))
		for _, old := range oldMatches {
			_ = os.Remove(old)
		}
		manifest.Frames = removeSourceFrames(manifest.Frames, rel)
		manifest.Sources = removeSourceRecords(manifest.Sources, rel)

		pattern := filepath.Join(imageDir, prefix+"%06d.jpg")
		filter := "fps=" + fpsText
		cmd := exec.Command(*ffmpeg,
			"-hide_banner", "-loglevel", "error", "-y",
			"-i", video,
			"-map", "0:v:0",
			"-an", "-sn", "-dn",
			"-vf", filter,
			"-q:v", strconv.Itoa(*quality),
			"-start_number", "0",
			pattern,
		)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "  extraction failed: %v: %s\n", err, strings.TrimSpace(stderr.String()))
			failed++
			continue
		}

		matches, _ := filepath.Glob(filepath.Join(imageDir, prefix+"*.jpg"))
		sort.Strings(matches)
		stream := firstVideoStream(probe)
		source := SourceRecord{
			Path: rel, Category: category, DurationSec: parseFloat(probe.Format.Duration),
			Width: stream.Width, Height: stream.Height, Codec: stream.CodecName,
			FrameRate: stream.AvgFrameRate, Frames: len(matches),
		}
		manifest.Sources = append(manifest.Sources, source)

		for frameIndex, imagePath := range matches {
			base := filepath.Base(imagePath)
			// FFmpeg starts numbering at zero; the index is therefore the
			// nominal sample number, not the original encoded frame number.
			timestamp := time.Duration(frameIndex) * *interval
			manifest.Frames = append(manifest.Frames, FrameRecord{
				Path:   filepath.ToSlash(filepath.Join("images", base)),
				Source: rel, Category: category,
				TimestampMS: timestamp.Milliseconds(), Frame: frameIndex,
				Width: stream.Width, Height: stream.Height, Codec: stream.CodecName,
			})
		}
		fmt.Printf("  extracted %d frame(s)\n", len(matches))
		success++
	}

	sort.Slice(manifest.Sources, func(i, j int) bool { return manifest.Sources[i].Path < manifest.Sources[j].Path })
	sort.Slice(manifest.Frames, func(i, j int) bool { return manifest.Frames[i].Path < manifest.Frames[j].Path })
	if err := writeJSON(manifestPath, manifest); err != nil {
		return err
	}

	total := 0
	for _, s := range manifest.Sources {
		total += s.Frames
	}
	fmt.Printf("\nDone: %d succeeded, %d failed; %d frame(s) in dataset\n", success, failed, total)
	fmt.Printf("COLMAP images: %s\nManifest:      %s\n", imageDir, manifestPath)
	if failed > 0 {
		return fmt.Errorf("%d video(s) failed; see messages above", failed)
	}
	return nil
}

func probeVideo(ffprobe, path string) (probeResult, error) {
	var result probeResult
	cmd := exec.Command(ffprobe, "-v", "error", "-show_entries",
		"format=duration,format_name,size,bit_rate:stream=codec_type,codec_name,width,height,avg_frame_rate,r_frame_rate,duration",
		"-of", "json", path)
	out, err := cmd.Output()
	if err != nil {
		return result, fmt.Errorf("ffprobe: %w", err)
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return result, err
	}
	if firstVideoStream(result).CodecType == "" {
		return result, errors.New("no video stream found")
	}
	return result, nil
}

func firstVideoStream(p probeResult) struct {
	CodecType    string `json:"codec_type"`
	CodecName    string `json:"codec_name"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	AvgFrameRate string `json:"avg_frame_rate"`
	Duration     string `json:"duration"`
} {
	for _, s := range p.Streams {
		if s.CodecType == "video" {
			return s
		}
	}
	return struct {
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
		Duration     string `json:"duration"`
	}{}
}

func inferCategory(rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, part := range parts[:len(parts)-1] {
		switch strings.ToLower(part) {
		case "aerial", "drone":
			return "aerial"
		case "ground", "pov", "walk":
			return "ground"
		case "railway", "rail", "train":
			return "railway"
		case "historical", "history", "archive":
			return "historical"
		}
	}
	return "unspecified"
}

func sourceID(rel string) string {
	base := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(base) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
		} else if !lastUnderscore && b.Len() > 0 {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	slug := strings.Trim(b.String(), "_")
	if slug == "" {
		slug = "video"
	}
	sum := sha256.Sum256([]byte(filepath.ToSlash(rel)))
	return slug + "_" + hex.EncodeToString(sum[:4])
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func loadManifest(path string) Manifest {
	var m Manifest
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

func removeSourceFrames(in []FrameRecord, source string) []FrameRecord {
	out := in[:0]
	for _, f := range in {
		if f.Source != source {
			out = append(out, f)
		}
	}
	return out
}

func removeSourceRecords(in []SourceRecord, source string) []SourceRecord {
	out := in[:0]
	for _, s := range in {
		if s.Path != source {
			out = append(out, s)
		}
	}
	return out
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
