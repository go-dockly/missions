package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "extract":
		err = runExtract(os.Args[2:])
	case "analyze":
		err = runAnalyze(os.Args[2:])
	case "select":
		err = runSelect(os.Args[2:])
	case "help", "-h", "--help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`collector — turn reference videos into a COLMAP-ready image selection

Usage:
  collector extract --input ./videos --output ./dataset [--interval 500ms]
  collector analyze --dataset ./dataset
  collector select  --dataset ./dataset [thresholds]

extract   samples frames into <dataset>/images and finds shot cuts
analyze   scores every frame (sharpness, exposure, flatness, hash); cached
select    splits sources into shots, rejects bad frames, keeps keyframes,
          drops cross-source duplicates; writes selection.json, selected.txt
          (for colmap --image_list_path) and sheet.html (contact sheet)

Run "collector <command> -h" for the options of a command.

Output:
  <dataset>/images/         Flat image directory suitable for COLMAP (never pruned)
  <dataset>/manifest.json   Source and per-frame provenance
  <dataset>/analysis.json   Per-frame scores
  <dataset>/selection.json  Decisions, with a reason for every rejected frame`)
}
