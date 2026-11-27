package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"
)

const analysisVersion = 2

type FrameAnalysis struct {
	Path string `json:"path"`
	// Size and ModTime identify the file the scores belong to, so a re-run
	// only measures frames that are new or were re-extracted.
	Size    int64 `json:"size"`
	ModTime int64 `json:"mod_time_ns"`
	Metrics
	Error string `json:"error,omitempty"`
}

type Analysis struct {
	Version     int             `json:"version"`
	GeneratedAt time.Time       `json:"generated_at"`
	WorkHeight  int             `json:"work_height"`
	Frames      []FrameAnalysis `json:"frames"`
}

func runAnalyze(args []string) error {
	flags := flag.NewFlagSet("analyze", flag.ContinueOnError)
	dataset := flags.String("dataset", "", "dataset directory written by extract (required)")
	workers := flags.Int("workers", runtime.NumCPU(), "parallel decoders")
	force := flags.Bool("force", false, "re-measure every frame, ignoring the cache")
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
	analysisPath := filepath.Join(*dataset, "analysis.json")
	cached := map[string]FrameAnalysis{}
	if !*force {
		if old, err := readAnalysis(analysisPath); err == nil && old.Version == analysisVersion && old.WorkHeight == workHeight {
			for _, f := range old.Frames {
				cached[f.Path] = f
			}
		}
	}

	results := make([]FrameAnalysis, len(manifest.Frames))
	var todo []int
	for i, fr := range manifest.Frames {
		info, err := os.Stat(filepath.Join(*dataset, filepath.FromSlash(fr.Path)))
		if err != nil {
			results[i] = FrameAnalysis{Path: fr.Path, Error: err.Error()}
			continue
		}
		if c, ok := cached[fr.Path]; ok && c.Error == "" && c.Size == info.Size() && c.ModTime == info.ModTime().UnixNano() {
			results[i] = c
			continue
		}
		results[i] = FrameAnalysis{Path: fr.Path, Size: info.Size(), ModTime: info.ModTime().UnixNano()}
		todo = append(todo, i)
	}
	fmt.Printf("%d frame(s): %d cached, %d to measure\n", len(results), len(results)-len(todo), len(todo))

	start := time.Now()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	for w := 0; w < max(1, *workers); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				m, err := measureFile(filepath.Join(*dataset, filepath.FromSlash(results[i].Path)))
				if err != nil {
					results[i].Error = err.Error()
				} else {
					results[i].Metrics = m
				}
				mu.Lock()
				done++
				if done%100 == 0 || done == len(todo) {
					fmt.Printf("  %d/%d\n", done, len(todo))
				}
				mu.Unlock()
			}
		}()
	}
	for _, i := range todo {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	failed := 0
	for _, r := range results {
		if r.Error != "" {
			failed++
			fmt.Fprintf(os.Stderr, "  %s: %s\n", r.Path, r.Error)
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Path < results[j].Path })
	out := Analysis{Version: analysisVersion, GeneratedAt: time.Now().UTC(), WorkHeight: workHeight, Frames: results}
	if err := writeJSON(analysisPath, out); err != nil {
		return err
	}
	fmt.Printf("Measured %d in %s; %d failed\nAnalysis: %s\n", len(todo), time.Since(start).Round(time.Millisecond), failed, analysisPath)
	return nil
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

func readAnalysis(path string) (Analysis, error) {
	var a Analysis
	data, err := os.ReadFile(path)
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(data, &a)
	return a, err
}
