# commandos missions reference collector

A small Go CLI for turning local video references into a timestamped image dataset for COLMAP.

## Requirements

- Go 1.23+
- FFmpeg
- FFprobe (normally installed with FFmpeg)

On macOS:

```bash
brew install ffmpeg
```

## Build

```bash
go build -o collector ./cmd/collector
```

## Expected input layout

Category inference uses directory names. These names are recognized:

```text
videos/
├── aerial/       # or drone/
├── ground/       # or pov/, walk/
├── railway/      # or rail/, train/
└── historical/   # or history/, archive/
```

Historical folders are skipped by default because archival footage should not be mixed into the modern photogrammetry reconstruction.

## Extract frames

```bash
./collector extract \
    --input ./missions/gorgopotamos/videos \
    --output ./missions/gorgopotamos/dataset \
    --interval 1s
```

Higher sampling density:

```bash
./collector extract \
  --input ./videos/aerial \
  --output ./dataset \
  --interval 500ms \
  --category aerial
```

Include historical folders only when explicitly requested:

```bash
./reference-collector extract \
  --input ./videos \
  --output ./dataset \
  --interval 2s \
  --include-historical
```

## Output

```text
gorgopotamos/
├── images/
│   ├── drone_01_a1b2c3d4_000000.jpg
│   ├── drone_01_a1b2c3d4_000001.jpg
│   └── ...
└── manifest.json
```

All images are placed in one flat directory, ready to pass to COLMAP. The manifest records the source-relative path, inferred category, nominal timestamp, image dimensions, codec and extracted frame count.

The manifest is updated incrementally. Re-running extraction for a source replaces that source's previous frames and records while retaining records for other sources.

## COLMAP

Example feature extraction:

```bash
colmap feature_extractor \
  --database_path ./missions/gorgopotamos/database.db \
  --image_path ./missions/gorgopotamos/images
```

Then run matching and mapping using the COLMAP workflow appropriate for the footage.

## Notes

- The sampling interval is a target interval, not a guarantee that every sampled image is useful for SfM.
- Video frame timestamps in the manifest are nominal sample timestamps (`frame index × interval`). They do not account for unusual source stream start-time offsets.
- This first version does not perform blur filtering, perceptual deduplication, scene-change detection or image selection. Those are deliberate follow-up steps: first inspect contact sheets and reconstruction quality before adding heuristics.
- Use only footage you are authorized to process. Public availability does not itself grant reuse or redistribution rights.
