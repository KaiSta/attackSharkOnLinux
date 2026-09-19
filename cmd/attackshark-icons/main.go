// Command attackshark-icons renders the tray icon states as a single PNG strip.
//
// It exists so the glyphs can be looked at without a running tray, and so the
// image in README.md can be regenerated if the drawing changes:
//
//	go run ./cmd/attackshark-icons -out docs/icon-states.png
//
// With -html it also writes a self-contained HTML page showing the same strip
// on a light and a dark background, which is how the glyph is judged against
// both kinds of panel:
//
//	go run ./cmd/attackshark-icons -size 22 -scale 8 -html /tmp/icons.html
//
// It is a preview tool only; the tray app draws its own icon.
package main

import (
	"bytes"
	"encoding/base64"
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"attackshark-x11/internal/batteryicon"
)

// preview states, in the order they appear in the strip.
var states = []struct {
	name  string
	state batteryicon.State
}{
	{"offline", batteryicon.State{Connected: false}},
	{"10%", batteryicon.State{Connected: true, Percent: 10}},
	{"25%", batteryicon.State{Connected: true, Percent: 25}},
	{"50%", batteryicon.State{Connected: true, Percent: 50}},
	{"78%", batteryicon.State{Connected: true, Percent: 78}},
	{"100%", batteryicon.State{Connected: true, Percent: 100}},
	{"charging 60%", batteryicon.State{Connected: true, Percent: 60, Charging: true}},
}

func main() {
	out := flag.String("out", "icon-states.png", "where to write the strip")
	size := flag.Int("size", 22, "tray icon edge length in pixels")
	scale := flag.Int("scale", 6, "integer upscale factor, so the glyphs are visible")
	htmlOut := flag.String("html", "", "also write a self-contained HTML preview page (empty writes none)")
	flag.Parse()

	if *size <= 0 || *scale <= 0 {
		fail("-size and -scale must be positive")
	}

	strip, err := renderStrip(*size, *scale)
	if err != nil {
		fail("%v", err)
	}

	if err := writePNG(*out, strip); err != nil {
		fail("%v", err)
	}

	names := make([]string, len(states))
	for i, entry := range states {
		names[i] = entry.name
	}
	fmt.Printf("wrote %s (%dx%d): %v\n", *out, strip.Bounds().Dx(), strip.Bounds().Dy(), names)

	if *htmlOut != "" {
		if err := writeHTMLPreview(*htmlOut, strip, *size); err != nil {
			fail("%v", err)
		}
		fmt.Printf("wrote %s (self-contained preview at %d px)\n", *htmlOut, *size)
	}
}

// renderStrip draws every state side by side, upscaled so the glyph is visible.
func renderStrip(size, scale int) (*image.NRGBA, error) {
	stride := size * scale
	// A transparent gutter keeps neighbouring glyphs distinct.
	gutter := stride / 6
	width := len(states)*stride + (len(states)-1)*gutter
	strip := image.NewNRGBA(image.Rect(0, 0, width, stride))

	for i, entry := range states {
		pngBytes, err := batteryicon.PNG(entry.state, size)
		if err != nil {
			return nil, fmt.Errorf("rendering %s: %w", entry.name, err)
		}
		icon, err := png.Decode(bytes.NewReader(pngBytes))
		if err != nil {
			return nil, fmt.Errorf("decoding %s: %w", entry.name, err)
		}
		scaled := upscale(icon, scale)
		draw.Draw(strip, scaled.Bounds().Add(image.Pt(i*(stride+gutter), 0)),
			scaled, image.Point{}, draw.Over)
	}
	return strip, nil
}

func writePNG(path string, img image.Image) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// writeHTMLPreview writes a page with the strip inlined as a data URI, so it
// stands alone: no sibling files, no server. That matters because the tray icon
// has to be legible on any panel colour, and the only way to check is to put
// the same glyph on both.
func writeHTMLPreview(path string, strip image.Image, size int) error {
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, strip); err != nil {
		return fmt.Errorf("encoding strip: %w", err)
	}
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes.Bytes())

	names := make([]string, len(states))
	for i, entry := range states {
		names[i] = entry.name
	}

	page := strings.NewReplacer(
		"{{URI}}", dataURI,
		"{{SIZE}}", fmt.Sprint(size),
		"{{NAMES}}", strings.Join(names, " · "),
	).Replace(htmlPreview)

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, []byte(page), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

const htmlPreview = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Attack Shark X11 - tray icon states</title>
<style>
  body { margin: 0; font: 13px/1.5 system-ui, sans-serif; }
  .band { padding: 16px 18px; }
  .light { background: #f2f2f2; color: #222; }
  .dark { background: #1b1b1b; color: #eee; }
  h1 { font-size: 14px; margin: 0 0 8px; font-weight: 600; }
  p { margin: 8px 0 0; font-size: 12px; opacity: .75; }
  img { display: block; width: min(100%, 960px); image-rendering: pixelated; }
</style>
</head>
<body>
<div class="band light">
  <h1>{{SIZE}} px tray icon states, light panel</h1>
  <img src="{{URI}}" alt="tray icon states">
</div>
<div class="band dark">
  <h1>{{SIZE}} px tray icon states, dark panel</h1>
  <img src="{{URI}}" alt="tray icon states">
</div>
<p style="padding: 0 18px 18px">Left to right: {{NAMES}}</p>
</body>
</html>
`

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

// upscale enlarges by an integer factor with nearest-neighbour sampling, which
// keeps the pixel grid honest rather than blurring it.
func upscale(src image.Image, factor int) *image.NRGBA {
	bounds := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, bounds.Dx()*factor, bounds.Dy()*factor))
	for y := 0; y < dst.Bounds().Dy(); y++ {
		for x := 0; x < dst.Bounds().Dx(); x++ {
			dst.Set(x, y, src.At(bounds.Min.X+x/factor, bounds.Min.Y+y/factor))
		}
	}
	return dst
}
