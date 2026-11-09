#!/usr/bin/env bash
# Render the README clips: each assets/<name>.tape is recorded with VHS to an
# mp4 under /tmp/memory-mcp-vhs, then converted to assets/<name>.webp.
#
# Usage (from anywhere): assets/render.sh [name ...]   # default: every tape
# Requires vhs, ffmpeg, img2webp (libwebp) and memory-mcp on PATH. Frames go
# through img2webp because Homebrew's ffmpeg is built without libwebp_anim.
set -euo pipefail

cd "$(dirname "$0")/.."
mkdir -p /tmp/memory-mcp-vhs

if [ $# -eq 0 ]; then
	set -- $(basename -s .tape assets/*.tape)
fi

for name in "$@"; do
	vhs "assets/$name.tape"
	frames="/tmp/memory-mcp-vhs/$name-frames"
	rm -rf "$frames" && mkdir -p "$frames"
	ffmpeg -y -v error -i "/tmp/memory-mcp-vhs/$name.mp4" -vf fps=20 "$frames/%04d.png"
	img2webp -loop 0 -lossy -q 70 -m 6 -d 50 "$frames"/*.png -o "assets/$name.webp" >/dev/null
	echo "assets/$name.webp: $(du -h "assets/$name.webp" | cut -f1)"
done
