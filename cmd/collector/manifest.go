package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// The manifest is extract's record of what is in images/: one entry per
// source video and per frame. analyze and select only read it.

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
	// Shot cuts in source time, from ffmpeg scdet at full frame rate.
	// CutsDetected tells "no cuts" apart from "never looked" (older manifests).
	CutsMS       []int64 `json:"cuts_ms,omitempty"`
	CutsDetected bool    `json:"cuts_detected,omitempty"`
}

type Manifest struct {
	Version     int            `json:"version"`
	GeneratedAt time.Time      `json:"generated_at"`
	Interval    string         `json:"interval"`
	Sources     []SourceRecord `json:"sources"`
	Frames      []FrameRecord  `json:"frames"`
}

// loadManifest is extract's reader: no manifest is a new dataset, but an
// unreadable one is an error rather than something to overwrite.
func loadManifest(path string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("%s: %w (fix or delete it)", path, err)
	}
	return m, nil
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

func readManifest(path string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(path)
	if err != nil {
		return m, fmt.Errorf("no manifest (run extract first): %w", err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}
