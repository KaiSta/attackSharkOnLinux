package batteryicon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestLevelBuckets(t *testing.T) {
	tests := []struct {
		percent int
		want    Level
	}{
		{0, LevelLow},
		{1, LevelLow},
		{24, LevelLow},
		{25, LevelLow}, // the boundary warns early rather than late
		{26, LevelMedium},
		{50, LevelMedium},
		{51, LevelHigh},
		{99, LevelHigh},
		{100, LevelHigh},
	}
	for _, tt := range tests {
		got := State{Connected: true, Percent: tt.percent}.Level()
		if got != tt.want {
			t.Errorf("percent %d: Level() = %v, want %v", tt.percent, got, tt.want)
		}
	}
}

func TestLevelOutOfRangeClamps(t *testing.T) {
	if got := (State{Connected: true, Percent: -10}).Level(); got != LevelLow {
		t.Errorf("negative percent: Level() = %v, want %v", got, LevelLow)
	}
	if got := (State{Connected: true, Percent: 300}).Level(); got != LevelHigh {
		t.Errorf("over 100 percent: Level() = %v, want %v", got, LevelHigh)
	}
}

// TestKeyTracksOnlyVisibleChanges pins the point of Key: it is a cache key, so it
// must change whenever the drawing changes and stay put when it does not. The
// drawing is a function of the colour bucket and the charging flag, so a few
// percent inside one bucket must not invalidate it.
func TestKeyTracksOnlyVisibleChanges(t *testing.T) {
	same := [][2]State{
		{{Connected: true, Percent: 10}, {Connected: true, Percent: 24}},
		{{Connected: true, Percent: 51}, {Connected: true, Percent: 100}},
		{{Connected: true, Percent: 90, Charging: true}, {Connected: true, Percent: 55, Charging: true}},
	}
	for _, pair := range same {
		if pair[0].Key() != pair[1].Key() {
			t.Errorf("%+v and %+v look the same but have keys %q and %q",
				pair[0], pair[1], pair[0].Key(), pair[1].Key())
		}
	}

	// Any change that alters the drawing must alter the key.
	base := State{Connected: true, Percent: 78}
	for _, changed := range []State{
		{Connected: false, Percent: 78},
		{Connected: true, Percent: 24},                 // green to red
		{Connected: true, Percent: 40},                 // green to amber
		{Connected: true, Percent: 78, Charging: true}, // the bolt
	} {
		if changed.Key() == base.Key() {
			t.Errorf("state %+v shares key %q with %+v", changed, base.Key(), base)
		}
	}
}

func TestPNGSize(t *testing.T) {
	for _, size := range []int{16, 24, 48, 96} {
		data, err := PNG(State{Connected: true, Percent: 60}, size)
		if err != nil {
			t.Fatalf("size %d: PNG returned error: %v", size, err)
		}
		img := decode(t, data)
		if got := img.Bounds().Dx(); got != size {
			t.Errorf("size %d: width = %d", size, got)
		}
		if got := img.Bounds().Dy(); got != size {
			t.Errorf("size %d: height = %d", size, got)
		}
	}
}

func TestPNGRejectsBadSizes(t *testing.T) {
	for _, size := range []int{0, -1, 100000} {
		if _, err := PNG(State{Connected: true, Percent: 50}, size); err == nil {
			t.Errorf("size %d: expected an error", size)
		}
	}
}

func TestPNGDeterministic(t *testing.T) {
	state := State{Connected: true, Percent: 42}
	first, err := PNG(state, 24)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PNG(state, 24)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Error("rendering the same state twice produced different bytes")
	}
}

func TestPNGBackgroundIsTransparent(t *testing.T) {
	img := decode(t, mustPNG(t, State{Connected: true, Percent: 80}, 48))
	for _, corner := range [][2]int{{0, 0}, {47, 0}, {0, 47}, {47, 47}} {
		if a := img.NRGBAAt(corner[0], corner[1]).A; a != 0 {
			t.Errorf("corner %v has alpha %d, want 0", corner, a)
		}
	}
	if countOpaque(img) == 0 {
		t.Error("the glyph drew nothing at all")
	}
}

// TestPNGStableWithinALevelBucket pins the deliberate simplicity of the design:
// within a colour bucket the icon does not change at all. The number lives in the
// tooltip; the glyph only answers "roughly how much is left?".
func TestPNGStableWithinALevelBucket(t *testing.T) {
	at := func(percent int) []byte {
		return mustPNG(t, State{Connected: true, Percent: percent}, 32)
	}

	if !bytes.Equal(at(10), at(24)) {
		t.Error("two red states rendered differently; the fill level should not affect the glyph")
	}
	if !bytes.Equal(at(51), at(100)) {
		t.Error("51% and 100% are both green but rendered differently")
	}
	if bytes.Equal(at(24), at(26)) {
		t.Error("crossing from red to amber did not change the glyph")
	}
	if bytes.Equal(at(50), at(51)) {
		t.Error("crossing from amber to green did not change the glyph")
	}
}

func TestPNGClampsOutOfRangePercent(t *testing.T) {
	high := mustPNG(t, State{Connected: true, Percent: 100}, 32)
	if !bytes.Equal(mustPNG(t, State{Connected: true, Percent: 400}, 32), high) {
		t.Error("percent above 100 was not clamped")
	}
	low := mustPNG(t, State{Connected: true, Percent: 0}, 32)
	if !bytes.Equal(mustPNG(t, State{Connected: true, Percent: -40}, 32), low) {
		t.Error("negative percent was not clamped")
	}
}

// TestPNGFinIsAlwaysDrawn pins the reason the fin replaced the earlier glyphs:
// it has to be bold and complete in every non-charging state, including empty and
// offline, because at tray size it is the whole icon.
func TestPNGFinIsAlwaysDrawn(t *testing.T) {
	states := []State{
		{Connected: false},
		{Connected: true, Percent: 0},
		{Connected: true, Percent: 25},
		{Connected: true, Percent: 60},
		{Connected: true, Percent: 100},
	}
	for _, s := range states {
		img := decode(t, mustPNG(t, s, 48))
		for _, p := range [][2]float64{{0.45, 0.25}, {0.40, 0.60}, {0.70, 0.80}} {
			if a := pixelAt(img, p[0], p[1]).A; a != 255 {
				t.Errorf("state %+v: the fin has alpha %d at (%.2f, %.2f), want a solid fin", s, a, p[0], p[1])
			}
		}
	}
}

// TestFinFillsTheIcon guards the complaint that prompted this design: at tray
// size the glyph must use the square rather than floating in the middle of it.
func TestFinFillsTheIcon(t *testing.T) {
	const size = 32
	img := decode(t, mustPNG(t, State{Connected: true, Percent: 80}, size))

	minX, minY, maxX, maxY := inkBounds(img)
	if width := maxX - minX; width < 0.75 {
		t.Errorf("the fin spans %.2f of the icon width, want at least 0.75", width)
	}
	if height := maxY - minY; height < 0.85 {
		t.Errorf("the fin spans %.2f of the icon height, want at least 0.85", height)
	}
}

// TestFinShape pins the geometry that makes the glyph read as a dorsal fin. It is
// checked on a drawn fin, where an opaque pixel can only be the fin itself.
func TestFinShape(t *testing.T) {
	const size = 96
	fin := decode(t, mustPNG(t, State{Connected: true, Percent: 50}, size))

	inside := []struct {
		name string
		x, y float64
	}{
		{"the upper sail", 0.45, 0.25},
		{"the mid sail", 0.40, 0.60},
		{"the wide base", 0.70, 0.85},
	}
	for _, p := range inside {
		if a := pixelAt(fin, p.x, p.y).A; a < 200 {
			t.Errorf("%s at (%.2f, %.2f) has alpha %d; the fin should cover it", p.name, p.x, p.y, a)
		}
	}

	// Open space around the fin: without these the glyph is a filled slab.
	outside := []struct {
		name string
		x, y float64
	}{
		{"the air ahead of the leading edge", 0.05, 0.20},
		{"the air above the trailing edge", 0.60, 0.20},
		{"the air ahead of the base", 0.05, 0.90},
		{"the air aft of the fin", 0.99, 0.60},
	}
	for _, p := range outside {
		if a := pixelAt(fin, p.x, p.y).A; a != 0 {
			t.Errorf("%s at (%.2f, %.2f) has alpha %d; it should be empty", p.name, p.x, p.y, a)
		}
	}
}

// TestChargingReplacesTheFinWithABolt pins the design decision: while plugged in
// the fin is gone and a bolt is drawn in its place, so the state is unmistakable
// at a glance.
func TestChargingReplacesTheFinWithABolt(t *testing.T) {
	const size = 96
	charging := decode(t, mustPNG(t, State{Connected: true, Percent: 50, Charging: true}, size))
	discharging := decode(t, mustPNG(t, State{Connected: true, Percent: 50}, size))

	// (0.563, 0.20) is on the bolt's upper stroke, and outside the fin, so only a
	// bolt can put ink there. (0.40, 0.60) is inside the fin and clear of the
	// bolt, so only the fin can.
	bolt := [2]float64{0.563, 0.20}
	fin := [2]float64{0.40, 0.60}

	if a := pixelAt(charging, bolt[0], bolt[1]).A; a < 200 {
		t.Errorf("the bolt has alpha %d where its upper stroke should be", a)
	}
	if a := pixelAt(discharging, bolt[0], bolt[1]).A; a != 0 {
		t.Errorf("the air beside the fin has alpha %d, want nothing", a)
	}
	if a := pixelAt(charging, fin[0], fin[1]).A; a != 0 {
		t.Errorf("the fin is still drawn at alpha %d while charging; the bolt should replace it", a)
	}
	if a := pixelAt(discharging, fin[0], fin[1]).A; a != 255 {
		t.Errorf("the fin has alpha %d while discharging, want a solid fin", a)
	}
}

func TestPNGDistinguishesEveryVisualState(t *testing.T) {
	states := []State{
		{Connected: false, Percent: 0},
		{Connected: true, Percent: 10},                 // red
		{Connected: true, Percent: 40},                 // amber
		{Connected: true, Percent: 90},                 // green
		{Connected: true, Percent: 90, Charging: true}, // green + bolt
		{Connected: true, Percent: 40, Charging: true}, // amber + bolt
		{Connected: true, Percent: 10, Charging: true}, // red + bolt
	}
	seen := map[string]State{}
	for _, s := range states {
		data := mustPNG(t, s, 32)
		key := string(data)
		if other, dup := seen[key]; dup {
			t.Errorf("states %+v and %+v render identically", s, other)
		}
		seen[key] = s
	}
}

func TestColourForMatchesLevel(t *testing.T) {
	for _, tc := range []struct {
		percent int
		want    color.NRGBA
	}{
		{10, colourLow},
		{25, colourLow},
		{40, colourMedium},
		{50, colourMedium},
		{80, colourHigh},
	} {
		if got := colourFor(State{Connected: true, Percent: tc.percent}); got != tc.want {
			t.Errorf("percent %d: colourFor = %+v, want %+v", tc.percent, got, tc.want)
		}
	}
}

func mustPNG(t *testing.T, s State, size int) []byte {
	t.Helper()
	data, err := PNG(s, size)
	if err != nil {
		t.Fatalf("PNG(%+v, %d): %v", s, size, err)
	}
	return data
}

func decode(t *testing.T, data []byte) *image.NRGBA {
	t.Helper()
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decoding generated PNG: %v", err)
	}
	bounds := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			out.SetNRGBA(x, y, color.NRGBAModel.Convert(src.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA))
		}
	}
	return out
}

func countOpaque(img *image.NRGBA) int {
	bounds := img.Bounds()
	count := 0
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			if img.NRGBAAt(x, y).A > 128 {
				count++
			}
		}
	}
	return count
}

// inkBounds is the bounding box of every pixel with any opacity at all, in
// normalised coordinates.
func inkBounds(img *image.NRGBA) (minX, minY, maxX, maxY float64) {
	bounds := img.Bounds()
	minX, minY = 1, 1
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			if img.NRGBAAt(x, y).A == 0 {
				continue
			}
			fx := float64(x) / float64(bounds.Dx())
			fy := float64(y) / float64(bounds.Dy())
			minX = min(minX, fx)
			minY = min(minY, fy)
			maxX = max(maxX, fx)
			maxY = max(maxY, fy)
		}
	}
	return minX, minY, maxX, maxY
}

func pixelAt(img *image.NRGBA, nx, ny float64) color.NRGBA {
	bounds := img.Bounds()
	x := int(nx * float64(bounds.Dx()))
	y := int(ny * float64(bounds.Dy()))
	if x >= bounds.Dx() {
		x = bounds.Dx() - 1
	}
	if y >= bounds.Dy() {
		y = bounds.Dy() - 1
	}
	return img.NRGBAAt(x, y)
}
