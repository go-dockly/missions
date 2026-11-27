# missions

Reference material for commandos missions, and `collector`: a Go CLI that turns reference videos into a small, sharp, non-redundant image set for COLMAP.

- `missions/<name>/`: mission notes, source links and datasets
- `docs/footage.md`: what footage helps a reconstruction, and how to mix sources
- `cmd/collector/`: the CLI (standard library only)

## Setup

```bash
brew install ffmpeg            # ffmpeg + ffprobe
go build -o collector ./cmd/collector
```

## Workflow

```bash
D=missions/gorgopotamos/dataset
./collector extract --input missions/gorgopotamos/videos --output $D   # frames + shot cuts
./collector analyze --dataset $D                                       # per-frame scores, cached
./collector select  --dataset $D                                       # decisions + contact sheet
open $D/sheet.html
```

Tune `select` by eye on the sheet and re-run it; it takes about a second and never touches `images/`. Re-run `analyze` after `extract` (it only measures new or changed frames).

## Layout

```text
videos/                  input; the folder name sets the category
├── aerial/              (or drone/)
├── ground/              (or pov/, walk/)
├── railway/             (or rail/, train/)
└── historical/          (or history/, archive/): skipped unless --include-historical
dataset/
├── images/              every sampled frame, flat: <source>_<hash>_<n>.jpg
├── manifest.json        sources, cuts and frames (extract)
├── analysis.json        per-frame scores and thumbnails (analyze)
├── selection.json       settings, per-source summary, a decision per frame (select)
├── selected.txt         kept image names, for colmap --image_list_path
└── sheet.html           contact sheet; open it from this folder
```

## extract

Samples every video under `--input` into `images/`, every 500 ms by default. That is denser than COLMAP needs on purpose, because `select` chooses keyframes by how far the view moved. A second full-rate pass finds shot cuts with FFmpeg's `scdet` filter.

| Flag | Default | |
| --- | --- | --- |
| `--interval` | `500ms` | try `250ms` for fast footage; `select` warns when a source needs it |
| `--category` | from folder | category for every video |
| `--include-historical` | off | archival footage doesn't belong in a modern reconstruction |
| `--cuts`, `--cut-threshold` | on, 10 | cut detection; lower finds more cuts |
| `--prune` | off | remove manifest sources whose video is no longer under `--input` |

Re-extracting a source replaces its frames. A source that was moved or renamed shows up as a warning, with a guess at where it went. `--prune` removes it. Pruning is opt-in, because extracting one subfolder into a shared dataset is normal.

## analyze

Decodes each frame once, at 360 px high so a 4K original and a 720p re-upload score alike. For each frame it measures sharpness (variance of the Laplacian), brightness, contrast, clipped pixels, flatness (the share of textureless blocks: sky, haze, fades, title cards), a luma histogram, and a 64×36 thumbnail. Results are cached by file size and modification time; `--force` re-measures everything.

## select

Every rejected frame gets a reason:

1. **Shots.** Frames are split at the cuts. A frame right at a cut moves to the side it matches.
2. **Quality.** `dark` (a fade; dim footage is fine), `bright`, `exposure`, `flat`, and `blur` (under half the sharpness of frames within ±2 s; motion blur is brief, soft content lasts).
3. **Keyframes.** Thumbnails are aligned to measure how far the view *shifted* and how much it *changed* otherwise (forward flight, turns). A window of frames lasts until the view has moved about 20%. The sharpest frame in each window is kept and the rest are `similar`. Kept neighbours overlap roughly 60–80%.
4. **Duplicates.** A kept frame showing the same view as a better one from another source, another shot or a later pass is a `duplicate`. Better means more pixels, then sharper.
5. **Gaps.** Consecutive samples too far apart to match get a warning: extract that source denser.

| Flag | Default | Raise it to... |
| --- | --- | --- |
| `--min-shift`, `--min-residual` | 0.2, 0.4 | keep fewer keyframes |
| `--blur` | 0.5 | reject more frames as blurry (share of nearby median) |
| `--min-sharpness` | off | drop a source that is soft throughout; the report's p10/p50/p90 per source show where |
| `--flat` | 0.85 | keep more sky and haze |
| `--dark`, `--bright`, `--clipped` | 12, 240, 0.4 | adjust the exposure rules |
| `--duplicate` | 0.25 | treat less similar views as duplicates; 0 turns the check off |
| `--gap-shift`, `--gap-residual` | 0.35, 0.6 | warn about gaps less often |

Err towards keeping. A frame kept wrongly costs little, because COLMAP drops what doesn't register. A frame rejected wrongly loses coverage.

## COLMAP

```bash
colmap feature_extractor --database_path $D/../database.db \
  --image_path $D/images --image_list_path $D/selected.txt
colmap sequential_matcher --database_path $D/../database.db
colmap mapper --database_path $D/../database.db --image_path $D/images --output_path $D/../sparse
```

## Limits

- Manifest timestamps are nominal (index × interval), not source timestamps.
- Alignment models one global shift. Forward flight and zoom are measured as residual, not as shift.
- The defaults are tuned on one 720p drone video and on synthetic footage.

## Next

- Zone tags per video or frame range, and a coverage report per zone
- Overrides: force-keep or force-reject frames from the sheet, kept across re-runs
- Video-level tags (season, weather, time of day), to keep reconstruction groups consistent
