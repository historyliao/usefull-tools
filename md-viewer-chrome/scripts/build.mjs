import { cp, mkdir, rm, copyFile } from "node:fs/promises";
import { context } from "esbuild";

const OUT = "dist";
const watch = process.argv.includes("--watch");

await rm(OUT, { recursive: true, force: true });
await mkdir(OUT, { recursive: true });

const options = {
  entryPoints: ["src/content.js", "src/background.js"],
  outdir: OUT,
  bundle: true,
  format: "iife",
  target: ["chrome120"],
  entryNames: "[name]",
  assetNames: "fonts/[name]",
  loader: { ".woff2": "file", ".woff": "file", ".ttf": "file" },
  minify: true,
  legalComments: "none",
  logLevel: "info",
};

const ctx = await context(options);
if (watch) {
  await ctx.watch();
  console.log("watching…");
} else {
  await ctx.rebuild();
  await ctx.dispose();
  await copyFile("src/manifest.json", `${OUT}/manifest.json`);
}

if (!watch) {
  await mkdir(`${OUT}/fonts`, { recursive: true });
  console.log(`built → ${OUT}/`);
}
