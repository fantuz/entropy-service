// The embedded HTML stays static; all labs take chart markup from one source.
import {readFileSync, writeFileSync} from "node:fs";
const root = new URL("../cmd/entropy-server/", import.meta.url);
const template = readFileSync(new URL("webtemplates/analysis-panels.html", root), "utf8");
const check = process.argv.includes("--check");
let stale = false;
function update(name, fragment, marker) {
    const url = new URL("web/" + name, root), html = readFileSync(url, "utf8");
    const start = `    ${marker === "ANALYSIS" ? "" : "  "}<!-- BEGIN SHARED ${marker} -->\n`;
    const end = `    ${marker === "ANALYSIS" ? "" : "  "}<!-- END SHARED ${marker} -->`;
    const a = html.indexOf(start), b = html.indexOf(end);
    if (a < 0 || b < a) throw new Error("Missing shared " + marker + " markers in " + name);
    const generated = html.slice(0, a + start.length) + fragment + html.slice(b);
    if (generated === html) return;
    if (check) { console.error(name + " shared " + marker + " is stale; run node scripts/render-analysis-pages.mjs"); stale = true; }
    else writeFileSync(url, generated);
}
for (const [name, values] of [
    ["api-stream.html", {trace_hint: "Pause, then click two bytes for A/B measurements.", waterfall_title: "Colour waterfall · sample history"}],
    ["api-sources.html", {trace_hint: "Freeze the plots, then click two bytes for A/B measurements.", waterfall_title: "Source waterfall · sample history"}],
    ["api-socket.html", {trace_hint: "Freeze the plots, then click two bytes for A/B measurements.", waterfall_title: "Socket waterfall · sample history"}],
    ["api-upload.html", {trace_hint: "Click two bytes for A/B measurements while playback is paused.", waterfall_title: "File waterfall · window history"}]
]) {
    const panels = template.replace(/\{\{([a-z_]+)\}\}/g, (_, key) => {
        if (!(key in values)) throw new Error("Unknown panel placeholder: " + key);
        return values[key];
    });
    update(name, panels, "ANALYSIS");
}
const help = readFileSync(new URL("webtemplates/lab-help.html", root), "utf8");
for (const name of ["api-sources.html", "api-socket.html"]) update(name, help, "LAB HELP");
if (stale) process.exitCode = 1;
