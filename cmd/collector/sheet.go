package main

import (
	"fmt"
	"html/template"
	"os"
)

type sheetFrame struct {
	Decision
	M     Metrics
	Class string
	Label string
	Time  string
}

type sheetShot struct {
	Index  int
	Kept   int
	Frames []sheetFrame
}

type sheetSource struct {
	SourceSummary
	ReasonText string
	ShotList   []sheetShot
}

type sheetData struct {
	Sel     Selection
	Reasons []string
	Counts  map[string]int
	Sources []sheetSource
}

// writeSheet writes a contact sheet next to images/, so image paths in the
// manifest work as they are. Kept frames are framed green, rejected ones are
// dimmed and framed by reason; hover shows the numbers behind the decision.
func writeSheet(path string, sel Selection) error {
	data := sheetData{Sel: sel, Reasons: reasonOrder, Counts: sel.Reasons}
	index := map[string]int{}
	for _, s := range sel.Sources {
		index[s.Path] = len(data.Sources)
		data.Sources = append(data.Sources, sheetSource{SourceSummary: s, ReasonText: reasonsText(s.Reasons)})
	}
	for _, d := range sel.Decisions {
		src := &data.Sources[index[d.Source]]
		for len(src.ShotList) <= d.Shot {
			src.ShotList = append(src.ShotList, sheetShot{Index: len(src.ShotList)})
		}
		f := sheetFrame{Decision: d, M: d.metrics, Class: "keep", Label: "kept"}
		if !d.Keep {
			f.Class, f.Label = d.Reason, d.Reason
		}
		sec := d.TimestampMS / 1000
		f.Time = fmt.Sprintf("%d:%02d.%d", sec/60, sec%60, (d.TimestampMS%1000)/100)
		shot := &src.ShotList[d.Shot]
		shot.Frames = append(shot.Frames, f)
		if d.Keep {
			shot.Kept++
		}
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := sheetTemplate.Execute(out, data); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

var sheetTemplate = template.Must(template.New("sheet").Funcs(template.FuncMap{
	"f0": func(v float64) string { return fmt.Sprintf("%.0f", v) },
	"f2": func(v float64) string { return fmt.Sprintf("%.2f", v) },
}).Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8">
<title>Contact sheet: {{.Sel.Kept}} of {{.Sel.Frames}} kept</title>
<style>
:root { --bg:#16181b; --fg:#e6e6e6; --dim:#8a8f98; --keep:#3fb950; --dark:#8957e5; --bright:#d2a8ff;
  --exposure:#bc8cff; --flat:#58a6ff; --blur:#f85149; --similar:#6e7681; --duplicate:#f0883e; --missing:#ff7b72; }
body { margin:0; padding:16px 20px 60px; background:var(--bg); color:var(--fg); font:13px/1.4 ui-monospace,Menlo,monospace; }
h1 { font-size:16px; margin:0 0 4px; } h2 { font-size:14px; margin:28px 0 2px; } h3 { font-size:12px; margin:14px 0 6px; color:var(--dim); font-weight:normal; }
.meta { color:var(--dim); }
.bar { position:sticky; top:0; background:var(--bg); padding:8px 0; z-index:2; display:flex; gap:14px; flex-wrap:wrap; align-items:center; border-bottom:1px solid #2a2e33; }
.bar label { cursor:pointer; user-select:none; }
.sw { display:inline-block; width:10px; height:10px; border-radius:2px; margin-right:4px; vertical-align:-1px; }
.grid { display:flex; flex-wrap:wrap; gap:6px; }
figure { margin:0; width:208px; border:3px solid; border-radius:4px; background:#000; position:relative; }
figure img { display:block; width:100%; aspect-ratio:16/9; object-fit:cover; }
figcaption { padding:2px 5px; font-size:11px; background:#0d0f11; display:flex; justify-content:space-between; }
figure:not(.keep) img { opacity:.4; }
figure:not(.keep):hover img { opacity:1; }
figure.warn::after { content:"⚠"; position:absolute; top:2px; right:6px; color:#f2cc60; text-shadow:0 0 3px #000; }
{{range .Reasons}}figure.{{.}} { border-color:var(--{{.}}); } .sw.{{.}} { background:var(--{{.}}); }
{{end}}figure.keep { border-color:var(--keep); } .sw.keep { background:var(--keep); }
body.hide-rejected figure:not(.keep) { display:none; }
{{range .Reasons}}body.hide-{{.}} figure.{{.}} { display:none; }
{{end}}
</style></head>
<body>
<h1>{{.Sel.Kept}} of {{.Sel.Frames}} frames kept</h1>
<div class="meta">generated {{.Sel.GeneratedAt.Format "2006-01-02 15:04"}} UTC ·
keyframe every {{f2 .Sel.Settings.MinShift}} shift / {{f2 .Sel.Settings.MinResidual}} residual · blur {{f2 .Sel.Settings.BlurRel}}× shot median · flat {{f2 .Sel.Settings.Flat}} · duplicate {{f2 .Sel.Settings.Dup}}</div>
<div class="bar">
  <label><input type="checkbox" id="rejected" checked> rejected</label>
  <span><span class="sw keep"></span>kept {{.Sel.Kept}}</span>
  {{range $r := .Reasons}}{{with index $.Counts $r}}<label><input type="checkbox" class="reason" value="{{$r}}" checked> <span class="sw {{$r}}"></span>{{$r}} {{.}}</label>{{end}}{{end}}
  <label>order <select id="order"><option value="time">time</option><option value="sharp">sharpness</option></select></label>
</div>
{{range .Sources}}
<h2>{{.Path}}</h2>
<div class="meta">{{.Category}} · {{.Resolution}} · {{.Frames}} frames · {{.Shots}} shots (cuts from {{.CutsFrom}}) · {{.Kept}} kept ·
rejected: {{.ReasonText}} · sharpness p10/p50/p90 {{f0 .SharpP10}}/{{f0 .SharpP50}}/{{f0 .SharpP90}}{{if .Gaps}} · ⚠ {{.Gaps}} jumps between samples{{end}}</div>
{{range .ShotList}}
<h3>shot {{.Index}} · {{len .Frames}} frames · {{.Kept}} kept</h3>
<div class="grid">{{range .Frames}}
<figure class="{{.Class}}{{if .Warn}} warn{{end}}" data-t="{{.TimestampMS}}" data-s="{{.Sharpness}}" title="{{.Path}}
{{.Label}}{{if .Ref}} → {{.Ref}}{{end}}{{if .Warn}}
{{.Warn}}{{end}}
sharpness {{f0 .Sharpness}} ({{f2 .RelSharpness}}× shot median)
brightness {{f0 .M.Brightness}} · contrast {{f0 .M.Contrast}} · clipped {{f2 .M.Clipped}} · flat {{f2 .M.Flat}}">
<a href="{{.Path}}" target="_blank"><img loading="lazy" src="{{.Path}}" alt=""></a>
<figcaption><span>#{{.Frame}} {{.Time}}</span><span>{{.Label}}</span></figcaption>
</figure>{{end}}
</div>{{end}}{{end}}
<script>
const body = document.body;
document.getElementById('rejected').addEventListener('change', e => body.classList.toggle('hide-rejected', !e.target.checked));
document.querySelectorAll('input.reason').forEach(cb => cb.addEventListener('change', () => body.classList.toggle('hide-' + cb.value, !cb.checked)));
document.getElementById('order').addEventListener('change', e => {
  const key = e.target.value === 'sharp' ? (f => -parseFloat(f.dataset.s)) : (f => parseFloat(f.dataset.t));
  document.querySelectorAll('.grid').forEach(g => [...g.children].sort((a, b) => key(a) - key(b)).forEach(f => g.appendChild(f)));
});
</script>
</body></html>
`))
