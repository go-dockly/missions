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

```bash
go build -o collector ./cmd/collector
D=missions/gorgopotamos/dataset
./collector extract --input missions/gorgopotamos/videos --output $D
./collector analyze --dataset $D
./collector select  --dataset $D && open $D/sheet.html
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

## Workflow

```bash
D=./missions/gorgopotamos
./collector extract --input $D/videos --output $D/dataset   # frames + shot cuts
./collector analyze --dataset $D/dataset                     # scores, cached
./collector select  --dataset $D/dataset                     # decisions + contact sheet
open $D/dataset/sheet.html
```

Tune `select` thresholds by eye on the sheet and re-run it; it takes seconds and never touches `images/`. Re-run `analyze` only after extracting new videos (it measures only new or changed frames).

## Extract frames

```bash
./collector extract \
    --input ./missions/gorgopotamos/videos \
    --output ./missions/gorgopotamos/dataset
```

The default interval is 500ms, denser than COLMAP needs: `select` picks keyframes by how far the view moved, so a hover gives one frame and a fast pass gives many. For very fast footage (train windows, low fast passes) try `--interval 250ms`; `select` says when a source needs it.

`extract` also finds shot cuts with FFmpeg's `scdet` filter in a second full-rate pass (`--cuts=false` skips it, `--cut-threshold` tunes it, default 10). Cuts go into the manifest (`cuts_ms`). Datasets extracted before this still work: `select` then finds cuts between frames instead.

Single category for a folder:

```bash
./collector extract \
  --input ./videos/aerial \
  --output ./dataset \
  --category aerial
```

Include historical folders only when explicitly requested:

```bash
./collector extract \
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

## Analyze

`analyze` decodes every frame once and stores, in `analysis.json`:

| Score | What | Measured |
| --- | --- | --- |
| sharpness | variance of the Laplacian | on luma scaled to 360 px high, so a 4K original and a 720p re-upload score alike |
| brightness, contrast | mean and deviation of luma | 0–255 |
| clipped | share of crushed (<8) or blown (>247) pixels | |
| flat | share of 8×8 blocks with almost no texture | sky, haze, fades, title-card backgrounds |
| histogram | 16-bin luma histogram | cut detection fallback |
| thumb | 64×36 luma thumbnail | everything about motion and duplicates; smoothed before comparing, so foliage moving under a pixel doesn't read as a new view |

## Select

`select` decides per frame and gives every rejected frame a reason:

1. **Shots.** Frames are grouped into shots by the cuts from `extract`. A frame right at a cut moves to the side it aligns with, because manifest times are nominal and `scdet`'s are the source's. Frames of different shots are never treated as neighbours.
2. **Quality**, per shot: `dark` (a fade; dim footage sits well above the threshold of 12), `bright`, `exposure` (clipped), `flat`, then `blur` when sharpness is below half the median of the frames within ±4 samples. Blur is judged against neighbours because motion blur lasts a few samples, while sharpness also follows content (a crisp truss, hazy distant slopes) for much longer.
3. **Keyframes**, per shot: two frames are compared by aligning their thumbnails (the best global shift, coarse to fine). That gives the *shift* (1 − overlap) and the *residual* left after aligning, which picks up forward flight, zoom, turns and parallax. A window of frames runs while the view stays within `--min-shift` 0.2 and `--min-residual` 0.4 of its first frame. The sharpest frame of each window is kept and the rest are `similar`, with a reference to the kept frame. Kept neighbours overlap roughly 60–80%.
4. **Duplicates** across shots and sources: a kept frame that aligns with a better kept frame (more pixels, then sharper) from another shot or source with residual below `--duplicate` 0.25 is a `duplicate`. This catches re-uploads, drone revisits and the famous angle twenty pilots filmed.
5. **Gaps**: consecutive samples that moved more than `--gap-shift` 0.35 or don't align (`--gap-residual` 0.6) get a warning. That source needs a denser `--interval`.

Output:

- `selection.json`: settings, per-source summary, and a decision per frame (`keep`, `reason`, `ref`, `warn`, `shot`).
- `selected.txt`: kept image names, for COLMAP's `--image_list_path`.
- `sheet.html`: contact sheet, by source and shot. Kept frames are framed green, rejected ones are dimmed and framed by reason. Hover shows the numbers. You can hide reasons or sort a shot by sharpness. Open it from the dataset folder (it links to `images/`).

The report printed by `select` gives each source's sharpness p10/p50/p90. Compare sources with it. To drop a source that is soft throughout, set `--min-sharpness` (absolute, off by default) between that source's p90 and the good sources' p10.

Defaults are starting points, tuned on synthetic footage only. Tune them on the sheet. A frame kept wrongly costs little, because COLMAP drops what doesn't register. A frame rejected wrongly loses coverage.

## COLMAP

Extract features for the selected frames only:

```bash
colmap feature_extractor \
  --database_path ./missions/gorgopotamos/database.db \
  --image_path ./missions/gorgopotamos/dataset/images \
  --image_list_path ./missions/gorgopotamos/dataset/selected.txt
```

Then run matching and mapping using the COLMAP workflow appropriate for the footage.

## Notes

- Video frame timestamps in the manifest are nominal sample timestamps (`frame index × interval`). They do not account for unusual source stream start-time offsets.
- `images/` and `manifest.json` are never changed by `analyze` or `select`; decisions live in `selection.json`.
- Alignment models one global shift. That is right for pans, orbits and sideways passes. Forward flight and zoom show up as residual instead, so they get keyframes by residual.

## Sorting & Filtering

we'll probably end up with hundreds of images of the bridge and comparatively few images of the gorge, slopes, forest and approaches.

**Recommendation: don't enforce uniformity of video quality, lighting, weather or season. Enforce geometric consistency within individual reconstruction groups, and diversity across the overall dataset.**

There is a big difference between reconstructing a 3D environment and creating a photorealistic panorama. We want the former.

## **1. What matters for photogrammetry?**

| Factor                     | Importance | Recommendation                      |
| -------------------------- | ---------- | ----------------------------------- |
| Camera viewpoint diversity | Critical   | Maximize                            |
| Image overlap              | Critical   | Maintain                            |
| Sharpness / motion blur    | Critical   | Reject blurry frames                |
| Exposure consistency       | High       | Prefer consistent within a sequence |
| Resolution                 | High       | Avoid extreme differences           |
| Weather                    | Medium     | Clear visibility preferred          |
| Time of day                | Medium     | Consistent within a sequence        |
| Season                     | Low–medium | Can mix, with precautions           |
| Sky appearance             | Low        | Usually not important               |

The most important thing is that the same physical features remain recognizable across images.

For example, a rock formation visible in 30 drone images from different angles is excellent.

A bridge visible in 300 images from practically the same angle is mostly redundant.

## **2. Can we mix seasons, weather and cameras?**

Yes, but I'd distinguish between reconstruction and appearance.

**Summer / lush vegetation**

Excellent for reconstructing vegetation and identifying paths, but foliage can obscure terrain.

Good source

**Autumn**

Different vegetation appearance and potentially better visibility through partially bare trees.

Good supplementary source

**Winter**

Can reveal terrain hidden by foliage, but snow and seasonal ground changes complicate matching.

Separate reconstruction group

**Overcast / cloudy**

Often excellent for photogrammetry because diffuse lighting reduces harsh shadows.

Good source

### **Rules for mixing material**

- Different cameras: absolutely fine.
- Different resolutions: fine, although avoid mixing tiny and very high-resolution images indiscriminately.
- Different white balances: generally fine.
- Different cloud conditions: fine.
- Different seasons: useful for additional reference, but don't blindly merge into one reconstruction.
- Snow-covered terrain versus snow-free terrain: keep separate.
- Changed vegetation, river levels or construction: treat as potentially different geometry.

One subtle issue: changing shadows can confuse feature matching. A rock in sunlight and the same rock under a completely different shadow may be less reliably matched.

## **3. The biggest danger: bridge bias**

I'd explicitly divide Gorgopotamos into geographical reconstruction zones.

Our dataset should cover:

## **Gorgopotamos coverage map**

Conceptual reconstruction zones, not measured boundaries.

# **01**

**Bridge**

Structure, piers, abutments, tracks

# **02**

**Northern gorge**

Rock faces, slopes, vegetation

# **03**

**Southern gorge**

Riverbanks, approaches, terrain

# **04**

**Western approach**

Paths, forest, elevated ground

# **05**

**Eastern approach**

Access routes and surrounding terrain

# **06**

**Riverbed**

Boulders, banks and water course

I would initially aim for something like this:

| Zone             | Target share of selected images |
| ---------------- | ------------------------------- |
| Bridge           | 20%                             |
| Northern gorge   | 20%                             |
| Southern gorge   | 20%                             |
| Western approach | 15%                             |
| Eastern approach | 15%                             |
| Riverbed         | 10%                             |

These aren't strict photogrammetry quotas. They're coverage targets to prevent the bridge from dominating our dataset.

Importantly, a single image can cover multiple zones.

## **4. We should improve the collector to understand coverage**

This is where I think we should invest next.

Currently, our collector knows:

- Source video.
- Timestamp.
- Resolution.
- Category.
- Frame number.

It doesn't know whether a frame is useful.

I'd introduce a richer metadata model:

```
type FrameMetadata struct {
    Source      string
    Timestamp   time.Duration

    Category    string
    Zone        string

    Width       int
    Height      int

    Sharpness   float64
    Brightness  float64

    PerceptualHash string

    Season      string
    Weather     string
    TimeOfDay   string

    Selected    bool
    Rejection   string
}
```

Not every field needs automatic detection. Zone, season and weather can initially be manually assigned to a video or frame range.

### **A particularly useful feature: contact-sheet review**

We should eventually have:

```
reference-collector analyze \
    --input ./gorgopotamos

reference-collector contact-sheet \
    --input ./gorgopotamos \
    --group-by source

reference-collector select \
    --input ./gorgopotamos \
    --max-similarity 0.95

reference-collector report \
    --input ./gorgopotamos
```

The report could show:

- Number of source videos.
- Total extracted frames.
- Frames by resolution.
- Frames by category.
- Frames by geographical zone.
- Blur distribution.
- Duplicate percentage.
- Estimated coverage gaps.

I'd keep the original extracted frames immutable and put selection decisions into a separate manifest. That way we can change selection strategies without repeatedly extracting everything.

## **5. One important distinction: COLMAP versus 3DGS**

We should not optimize both for exactly the same thing.

**COLMAP / photogrammetry**

Wants stable visual features, sufficient overlap, sharpness and geometrically consistent viewpoints.

**3D Gaussian Splatting**

Benefits from diverse camera positions and good visibility of the scene, but can suffer when images depict different seasons, vegetation states or lighting conditions.

My preference:

1. Reconstruct geometry from the best consistent image groups.
2. Align the separate reconstructions.
3. Use geometry to establish a common world.
4. Select a visually consistent subset for Gaussian splatting.
5. Use additional seasonal footage as reference for vegetation and environmental assets.

We should not let a beautiful but geometrically inconsistent image contaminate the foundation.

## **6. What I would implement next**

Recommended next milestone

**Reference Collector v0.2 — Dataset Intelligence**

| Feature                                 | Priority | Status |
| --------------------------------------- | -------- | ------ |
| Perceptual duplicate detection          | P0       | done (aligned thumbnails) |
| Blur detection                          | P0       | done |
| Contact sheet generation                | P0       | done (`sheet.html`) |
| Video-level metadata/tags               | P0       | |
| Frame selection manifest                | P1       | done (`selection.json`) |
| Geographical zone tagging               | P1       | |
| Coverage report                         | P1       | |
| Automatic season/weather classification | P2       | |

One further thought: let's avoid throwing away images just because they look similar. A stationary camera can still capture useful geometry if the subject moves, although moving objects themselves should generally be masked or excluded from reconstruction.

**The guiding principle for Gorgopotamos should be: maximize spatial coverage, not frame count.**

I'd rather have 800 sharp images covering the entire gorge from diverse viewpoints than 8,000 images of the railway bridge.

We could split it into three stages:
1. Quality analysis
   - Detect blurry frames using Laplacian variance.
   - Detect exact and perceptual duplicates.
   - Record resolution and image dimensions.
2. Visual review
   - Generate contact sheets grouped by source video.
   - Sort frames by quality.
   - Mark the sharpest frame in each near-duplicate group as the preferred candidate.
3. Coverage review
   - Organise aerial, ground and railway material.
   - Tag geographic zones: bridge, north gorge, south gorge, west approach, east approach and riverbed.
   - Avoid selecting only the prettiest images at the expense of spatial coverage.