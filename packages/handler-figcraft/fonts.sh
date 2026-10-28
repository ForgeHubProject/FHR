#!/bin/sh
# The fonts the FigCraft handler draws text with: Inter, from a
# storytold/craft-fonts checkout, as a folder FigCraft's text crate embeds when
# CRAFT_FONTS_DIR names it at build time. One small set on every target, and no
# system fonts (preview.rs), so a file draws the same wherever it is previewed.
# Other families fall back to Inter. Noto Sans Georgian comes along because
# FigCraft's text crate requires it in a wasm build that requires fonts. All of
# craft-fonts' fonts are OFL-1.1.
#
#   sh fonts.sh <craft-fonts checkout> <out dir>
#   CRAFT_FONTS_DIR=<out dir> CRAFT_FONTS_REQUIRED=1 cargo build --release -p handler-figcraft
set -eu
src=$1
out=$2
mkdir -p "$out/fonts/inter" "$out/fonts/noto-sans-georgian"
cp "$src"/fonts/inter/Inter-*.ttf "$src/fonts/inter/OFL.txt" "$out/fonts/inter/"
cp "$src"/fonts/noto-sans-georgian/* "$out/fonts/noto-sans-georgian/"
grep '^Inter |' "$src/fonts/latin-manifest.txt" > "$out/fonts/latin-manifest.txt"
grep '^Noto Sans Georgian |' "$src/fonts/manifest.txt" > "$out/fonts/manifest.txt"
echo "fonts in $out: $(cat "$out"/fonts/*manifest.txt | grep -c .) faces"
