# Footage for reconstruction

The goal is to reconstruct a 3D place, not to make a panorama. Maximise spatial coverage, not frame count: 800 sharp images covering a whole gorge from many viewpoints beat 8,000 of the bridge.

## What matters

| Factor | Importance | Aim |
| --- | --- | --- |
| Viewpoint diversity | critical | maximise |
| Overlap between neighbours | critical | keep it (60–80%) |
| Sharpness | critical | reject motion blur |
| Exposure within a sequence | high | consistent |
| Resolution | high | avoid extremes side by side |
| Weather, time of day | medium | clear and consistent within a sequence |
| Season | low–medium | mix with care (below) |
| Sky | low | irrelevant |

The same physical features must stay recognisable across images. A rock face seen in 30 images from different angles is excellent; a bridge seen in 300 images from one angle is mostly redundant.

## Mixing sources

- Cameras, resolutions, white balance, cloud: fine to mix.
- Overcast is often best, because diffuse light means no hard shadows to confuse matching.
- Seasons: summer foliage hides terrain, while autumn and winter reveal it. Use them as reference, but don't merge them blindly into one reconstruction.
- Snow vs. no snow, different river levels, vegetation or construction: treat as different geometry and reconstruct separately.

## Reconstruction plan

COLMAP needs stable features, overlap and consistent geometry. Gaussian splatting needs diverse camera positions but suffers from mixed seasons and lighting. So:

1. Reconstruct geometry from the best consistent groups of images.
2. Align the separate reconstructions into one world.
3. Pick a visually consistent subset for splatting.
4. Keep other seasons' footage as reference for vegetation and assets.

Never let a beautiful but geometrically inconsistent image into the foundation.

## Coverage

Famous subjects pull a dataset towards one view (Gorgopotamos: the bridge). Split each location into zones, set rough target shares, and check them per zone. A frame can count for several zones. Zones belong in the mission's README.
