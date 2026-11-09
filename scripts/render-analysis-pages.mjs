// The embedded HTML stays static; both labs take chart markup from one source.
import {readFileSync, writeFileSync} from "node:fs";
const root = new URL("../cmd/entropy-server/", import.meta.url);
const template = readFileSync(new URL("webtemplates/analysis-panels.html", root), "utf8");
const check = process.argv.includes("--check");
let stale = false;
for (const [name, values] of [
    ["api-stream.html", {trace_hint: "Pause, then click two bytes for A/B measurements.", waterfall_title: "Colour waterfall · sample history"}],
    ["api-upload.html", {trace_hint: "Click two bytes for A/B measurements while playback is paused.", waterfall_title: "File waterfall · window history"}]
]) {
    const url = new URL("web/" + name, root), html = readFileSync(url, "utf8");
    const start = "    <!-- BEGIN SHARED ANALYSIS -->\n", end = "    <!-- END SHARED ANALYSIS -->";
    const a = html.indexOf(start), b = html.indexOf(end);
    if (a < 0 || b < a) throw new Error("Missing shared panel markers in " + name);
    const panels = template.replace(/\{\{([a-z_]+)\}\}/g, (_, key) => {
        if (!(key in values)) throw new Error("Unknown panel placeholder: " + key);
        return values[key];
    });
    const generated = html.slice(0, a + start.length) + panels + html.slice(b);
    if (generated === html) continue;
    if (check) { console.error(name + " shared panels are stale; run node scripts/render-analysis-pages.mjs"); stale = true; }
    else writeFileSync(url, generated);
}
if (stale) process.exitCode = 1;
