// Package batteryicon draws the tray icon: a single shark's dorsal fin whose
// colour is the battery state - green when healthy, amber when it is getting
// low, red when it is nearly empty - and a lightning bolt instead of the fin
// while the mouse is charging. The exact percentage always lives in the tray
// tooltip, so the glyph only has to answer "roughly how much is left?" and "is
// it plugged in?" at a glance.
//
// The glyph is drawn from scratch at runtime with only the standard library - no
// image assets, no font, no cairo. Edges are antialiased by rendering at 4x and
// box-downsampling.
package batteryicon

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
)

// Level buckets the charge percentage into the three states the tray shows.
type Level int

const (
	// LevelUnknown is the zero value and is never produced for a connected
	// mouse.
	LevelUnknown Level = iota
	// LevelLow is 25% and below: red.
	LevelLow
	// LevelMedium is above 25% and up to 50%: amber.
	LevelMedium
	// LevelHigh is above 50%: green.
	LevelHigh
)

const (
	// LowThreshold is the highest percentage still shown as red. Exactly 25 is
	// treated as low, so the warning arrives one step earlier rather than one
	// step later.
	LowThreshold = 25
	// HighThreshold is the highest percentage still shown as amber. Exactly 50
	// is amber; 51 and above is green.
	HighThreshold = 50
)

func (l Level) String() string {
	switch l {
	case LevelLow:
		return "low"
	case LevelMedium:
		return "medium"
	case LevelHigh:
		return "high"
	default:
		return "unknown"
	}
}

// State is everything that affects what the icon looks like.
type State struct {
	// Connected is false when the receiver is unplugged or the mouse is off,
	// which draws the fin faintly in grey.
	Connected bool
	// Percent is the raw battery percentage as reported by the mouse.
	Percent int
	// Charging replaces the fin with a lightning bolt: while the mouse is plugged
	// in, that matters more at a glance than roughly how full it is.
	Charging bool
}

// Level returns the colour bucket for this state.
func (s State) Level() Level {
	switch p := s.clampedPercent(); {
	case p <= LowThreshold:
		return LevelLow
	case p <= HighThreshold:
		return LevelMedium
	default:
		return LevelHigh
	}
}

// Key identifies the rendered appearance, so the caller can skip regenerating the
// PNG when nothing visible has changed. The mouse reports every two seconds; the
// icon only has to be redrawn when it crosses a colour threshold, starts or
// stops charging, or comes and goes.
func (s State) Key() string {
	if !s.Connected {
		return "offline"
	}
	if s.Charging {
		return fmt.Sprintf("%s:charging", s.Level())
	}
	return s.Level().String()
}

func (s State) clampedPercent() int {
	switch {
	case s.Percent < 0:
		return 0
	case s.Percent > 100:
		return 100
	default:
		return s.Percent
	}
}

var (
	colourLow     = color.NRGBA{R: 0xd6, G: 0x45, B: 0x45, A: 0xff}
	colourMedium  = color.NRGBA{R: 0xe0, G: 0xa9, B: 0x3b, A: 0xff}
	colourHigh    = color.NRGBA{R: 0x3f, G: 0xa4, B: 0x5b, A: 0xff}
	colourOffline = color.NRGBA{R: 0x8a, G: 0x8a, B: 0x8a, A: 0xff}
)

// colourFor picks the glyph colour. The three levels are mid-tone on purpose so
// the icon stays legible on both light and dark panels.
func colourFor(s State) color.NRGBA {
	switch s.Level() {
	case LevelLow:
		return colourLow
	case LevelMedium:
		return colourMedium
	default:
		return colourHigh
	}
}

// Geometry is in normalised canvas units (0..1), so the glyph scales to any size,
// with y increasing downward.

// finOutline is the dorsal fin, and the whole subject of the icon: a Jaws-style
// silhouette with the tip leaning forward, a concave leading edge, a convex
// trailing sweep, and a flat base at the waterline, sized to fill the tray
// square rather than floating in the middle of it. The two curved edges are
// quadratic Béziers sampled into a polygon, so the outline stays smooth at
// every tray size. The proportions follow the reference silhouette.
func finOutline() [][2]float64 {
	tip := [2]float64{0.34, 0.06}
	baseLeft := [2]float64{0.12, 0.95}
	baseRight := [2]float64{0.90, 0.95}
	leadCtrl := [2]float64{0.46, 0.50}  // pulls the leading edge inward: concave
	trailCtrl := [2]float64{0.76, 0.44} // pulls the trailing edge outward: convex

	const samples = 16
	pts := make([][2]float64, 0, 2*samples+1)
	// Leading edge from the tip down to the base, then the trailing edge back
	// up to the tip; the flat base closes the loop.
	for i := 0; i <= samples; i++ {
		pts = append(pts, quadPoint(tip, leadCtrl, baseLeft, float64(i)/samples))
	}
	for i := 1; i <= samples; i++ {
		pts = append(pts, quadPoint(baseRight, trailCtrl, tip, float64(i)/samples))
	}
	return pts
}

// quadPoint is the point at parameter t on the quadratic Bézier from p0 through
// control point c to p1.
func quadPoint(p0, c, p1 [2]float64, t float64) [2]float64 {
	u := 1 - t
	return [2]float64{
		u*u*p0[0] + 2*u*t*c[0] + t*t*p1[0],
		u*u*p0[1] + 2*u*t*c[1] + t*t*p1[1],
	}
}

// boltPath is the lightning bolt drawn while charging, as a zigzag stroked at
// boltWidth. A stroked zigzag rather than a filled bolt outline, because at
// 22 px a filled bolt's waist pinches to nothing and reads as a sparkle, while a
// thick zigzag stays unmistakably a bolt.
var boltPath = [][2]float64{
	{0.600, 0.14},
	{0.380, 0.50},
	{0.560, 0.50},
	{0.420, 0.86},
}

// boltWidth is the stroke width of the bolt, in canvas units. Together with the
// path above it sizes the bolt to fill the tray square.
const boltWidth = 0.15

// supersample is the render scale; downsampling by this factor antialiases every
// edge, including the fin's outline and the bolt's diagonals.
const supersample = 4

// PNG renders the state as a square PNG, size pixels on a side, with a
// transparent background. Rendering the same state twice returns identical bytes.
func PNG(s State, size int) ([]byte, error) {
	if size <= 0 {
		return nil, fmt.Errorf("batteryicon: size must be positive, got %d", size)
	}
	if size > 512 {
		return nil, fmt.Errorf("batteryicon: size %d is unreasonably large", size)
	}

	canvas := size * supersample
	img := image.NewNRGBA(image.Rect(0, 0, canvas, canvas))

	if !s.Connected {
		drawGlyph(img, colourOffline, 0, false)
	} else {
		drawGlyph(img, colourFor(s), s.clampedPercent(), s.Charging)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, downscale(img, supersample)); err != nil {
		return nil, fmt.Errorf("batteryicon: encoding PNG: %w", err)
	}
	return buf.Bytes(), nil
}

// drawGlyph draws the one shape the state calls for: the bolt while charging,
// the fin otherwise. The colour carries the level either way.
func drawGlyph(img *image.NRGBA, c color.NRGBA, percent int, charging bool) {
	if charging {
		strokePath(img, boltPath, boltWidth, c, false)
		return
	}
	fillPolygon(img, finOutline(), c)
}

// fillPolygon fills a closed polygon given in normalised coordinates, using the
// even-odd rule on pixel centres.
func fillPolygon(img *image.NRGBA, poly [][2]float64, c color.NRGBA) {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()

	for y := 0; y < h; y++ {
		py := (float64(y) + 0.5) / float64(h)
		for x := 0; x < w; x++ {
			px := (float64(x) + 0.5) / float64(w)
			if pointInPolygon(px, py, poly) {
				img.SetNRGBA(x, y, c)
			}
		}
	}
}

// strokePath draws a polyline as quads of the given width, with a disc at each
// joint so the acute turns of a zigzag do not leave notches. Set closed to trace
// the path as a loop, and pass a fully transparent colour to erase instead of
// draw.
func strokePath(img *image.NRGBA, path [][2]float64, width float64, c color.NRGBA, closed bool) {
	half := width / 2
	segments := len(path) - 1
	if closed {
		segments = len(path)
	}

	for i := 0; i < segments; i++ {
		x0, y0 := path[i][0], path[i][1]
		x1, y1 := path[(i+1)%len(path)][0], path[(i+1)%len(path)][1]
		dx, dy := x1-x0, y1-y0
		length := math.Hypot(dx, dy)
		if length == 0 {
			continue
		}
		// Offset perpendicular to the segment, in canvas units.
		ox, oy := -dy/length*half, dx/length*half
		fillPolygon(img, [][2]float64{
			{x0 + ox, y0 + oy},
			{x1 + ox, y1 + oy},
			{x1 - ox, y1 - oy},
			{x0 - ox, y0 - oy},
		}, c)

		// Disc at the joint, unless this is the flat end of an open path.
		if closed || i < len(path)-2 {
			fillPolygon(img, disc(x1, y1, half), c)
		}
	}
}

// disc approximates a circle centred on (cx, cy) with a polygon, which is enough
// at this size and keeps the even-odd rasteriser as the only filler.
func disc(cx, cy, radius float64) [][2]float64 {
	const sides = 12
	points := make([][2]float64, 0, sides)
	for i := 0; i < sides; i++ {
		theta := 2 * math.Pi * float64(i) / sides
		points = append(points, [2]float64{
			cx + radius*math.Cos(theta),
			cy + radius*math.Sin(theta),
		})
	}
	return points
}

func pointInPolygon(x, y float64, poly [][2]float64) bool {
	inside := false
	n := len(poly)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		yi, yj := poly[i][1], poly[j][1]
		if (yi > y) != (yj > y) {
			xCross := (poly[j][0]-poly[i][0])*(y-yi)/(yj-yi) + poly[i][0]
			if x < xCross {
				inside = !inside
			}
		}
	}
	return inside
}

// downscale box-filters src by an integer factor. Averaging happens in
// premultiplied space so edges do not pick up dark halos against transparency.
func downscale(src *image.NRGBA, factor int) *image.NRGBA {
	bounds := src.Bounds()
	w := bounds.Dx() / factor
	h := bounds.Dy() / factor
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	samples := factor * factor

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var sumA, sumR, sumG, sumB int
			for dy := 0; dy < factor; dy++ {
				for dx := 0; dx < factor; dx++ {
					c := src.NRGBAAt(x*factor+dx, y*factor+dy)
					a := int(c.A)
					sumA += a
					sumR += int(c.R) * a / 255
					sumG += int(c.G) * a / 255
					sumB += int(c.B) * a / 255
				}
			}
			out := color.NRGBA{}
			if sumA > 0 {
				// Unpremultiplying cancels the sample count, leaving
				// sumR*255/sumA directly.
				out.R = clampByte(sumR * 255 / sumA)
				out.G = clampByte(sumG * 255 / sumA)
				out.B = clampByte(sumB * 255 / sumA)
			}
			out.A = uint8(clampInt(sumA/samples, 0, 255))
			dst.SetNRGBA(x, y, out)
		}
	}
	return dst
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampByte(v int) uint8 { return uint8(clampInt(v, 0, 255)) }
