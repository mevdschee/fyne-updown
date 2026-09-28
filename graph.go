package main

import (
	"image"
	"image/color"
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	// historySize is the number of samples kept per adapter, one per second
	historySize = 3600
	// sampleWidth is the width of a sample in the graph
	sampleWidth = 2
	// markerPeriod is the time between the time markers, aligned with the
	// clock
	markerPeriod = 10 * time.Second
)

// The graph colors differ a fixed perceived lightness (L*) from the
// background, a fixed gray would stand out more in dark mode.
const (
	// gridContrast is used for the bandwidth lines and the time markers
	gridContrast = 7
	// minuteContrast stands out more, it marks the start of each minute
	minuteContrast = 14
	axisContrast   = 40
	labelContrast  = 50
)

// history holds the latest rates of an adapter in bytes per second, oldest
// first.
type history struct {
	down, up []float64
	times    []time.Time
}

func (h *history) add(t time.Time, down, up float64) {
	if len(h.down) == historySize {
		h.down, h.up, h.times = h.down[1:], h.up[1:], h.times[1:]
	}
	h.down = append(h.down, down)
	h.up = append(h.up, up)
	h.times = append(h.times, t)
}

// timeAt returns the time of the sample i seconds before the latest one,
// beyond the oldest sample it counts back one second per sample.
func (h *history) timeAt(i int) time.Time {
	n := len(h.times)
	if i < n {
		return h.times[n-1-i]
	}
	return h.times[0].Add(-time.Duration(i-n+1) * time.Second)
}

type marker struct {
	// column counts from the right, the marker is on its left edge
	column int
	minute bool
	time   time.Time
}

// markers returns the clock aligned time markers within the given number of
// columns, where a column starts a new period of 10 seconds.
func (h *history) markers(columns int) []marker {
	var markers []marker
	if len(h.times) == 0 {
		return nil
	}
	for i := 0; i < columns; i++ {
		t, prev := h.timeAt(i), h.timeAt(i+1)
		if t.Truncate(markerPeriod).Equal(prev.Truncate(markerPeriod)) {
			continue
		}
		minute := t.Truncate(time.Minute)
		markers = append(markers, marker{i, !minute.Equal(prev.Truncate(time.Minute)), minute})
	}
	return markers
}

// graph draws the history of an adapter like btop does: download above the
// axis and upload below it. Both halves have the same height and each uses
// its own scale, a half stays empty when its scale is unknown.
type graph struct {
	widget.BaseWidget
	raster           *canvas.Raster
	downText, upText *canvas.Text
	// times labels the minute markers below the axis
	times              *fyne.Container
	samples            *history
	scaleDown, scaleUp float64 // bits per second
}

func newGraph() *graph {
	g := &graph{}
	g.raster = canvas.NewRaster(g.draw)
	g.downText = canvas.NewText("", color.Black)
	g.downText.TextSize = theme.CaptionTextSize()
	g.upText = canvas.NewText("", color.Black)
	g.upText.TextSize = theme.CaptionTextSize()
	g.times = container.NewWithoutLayout()
	g.ExtendBaseWidget(g)
	return g
}

func (g *graph) CreateRenderer() fyne.WidgetRenderer {
	pad := theme.InnerPadding()
	labels := container.New(layout.NewCustomPaddedLayout(pad/2, pad/2, pad, pad), container.NewBorder(g.downText, g.upText, nil, nil))
	return widget.NewSimpleRenderer(container.NewStack(g.raster, g.times, labels))
}

func (g *graph) Resize(size fyne.Size) {
	g.BaseWidget.Resize(size)
	g.placeTimes()
}

// placeTimes puts a 24h HH:MM label right of each minute marker, just below
// the axis.
func (g *graph) placeTimes() {
	size := g.Size()
	var objects []fyne.CanvasObject
	if g.samples != nil {
		for _, m := range g.samples.markers(int(size.Width / sampleWidth)) {
			if !m.minute {
				continue
			}
			text := canvas.NewText(m.time.Format("15:04"), themeGray(labelContrast))
			text.TextSize = theme.CaptionTextSize()
			x := size.Width - float32(m.column+1)*sampleWidth + theme.Padding()
			if x+text.MinSize().Width > size.Width {
				continue
			}
			text.Move(fyne.NewPos(x, size.Height/2+1))
			text.Resize(text.MinSize())
			objects = append(objects, text)
		}
	}
	g.times.Objects = objects
	g.times.Refresh()
}

func (g *graph) MinSize() fyne.Size {
	return fyne.NewSize(100, 120)
}

func (g *graph) set(samples *history, scaleDown, scaleUp float64) {
	g.samples, g.scaleDown, g.scaleUp = samples, scaleDown, scaleUp
	g.downText.Text = "Down " + formatScale(scaleDown)
	g.upText.Text = "Up " + formatScale(scaleUp)
	g.downText.Color = themeGray(labelContrast)
	g.upText.Color = themeGray(labelContrast)
	g.downText.Refresh()
	g.upText.Refresh()
	g.placeTimes()
	g.raster.Refresh()
}

func (g *graph) draw(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if g.samples == nil || w == 0 || h == 0 {
		return img
	}
	axis := h / 2
	// pixels per byte per second, zero leaves the half empty
	var downPixels, upPixels float64
	if g.scaleDown > 0 {
		downPixels = float64(axis) * 8 / g.scaleDown
	}
	if g.scaleUp > 0 {
		upPixels = float64(h-axis) * 8 / g.scaleUp
	}
	col := sampleWidth
	if size := g.Size(); size.Width > 0 {
		col = max(1, int(math.Round(float64(w)/float64(size.Width)*sampleWidth)))
	}
	n := len(g.samples.down)
	grid := themeGray(gridContrast)
	// lines at 25, 50, 75 and 100% of the download and upload scale
	for q := 1; q <= 4; q++ {
		down := axis - int(math.Round(float64(axis)*float64(q)/4))
		up := axis + int(math.Round(float64(h-axis)*float64(q)/4))
		fill(img, image.Rect(0, down, w, down+1), grid)
		fill(img, image.Rect(0, min(up, h-1), w, min(up, h-1)+1), grid)
	}
	for _, m := range g.samples.markers((w + col - 1) / col) {
		x := max(0, w-(m.column+1)*col)
		c := grid
		if m.minute {
			c = themeGray(minuteContrast)
		}
		fill(img, image.Rect(x, 0, x+1, h), c)
	}
	for i := 0; i < n && w-i*col > 0; i++ {
		x1 := w - i*col
		x0 := max(0, x1-col)
		down := min(axis, int(math.Round(g.samples.down[n-1-i]*downPixels)))
		up := min(h-axis, int(math.Round(g.samples.up[n-1-i]*upPixels)))
		for y := axis - down; y < axis; y++ {
			c := gradient(downDark, downBright, float64(axis-y)/float64(axis))
			fill(img, image.Rect(x0, y, x1, y+1), c)
		}
		for y := axis; y < axis+up; y++ {
			c := gradient(upDark, upBright, float64(y-axis+1)/float64(h-axis))
			fill(img, image.Rect(x0, y, x1, y+1), c)
		}
	}
	fill(img, image.Rect(0, axis, w, axis+1), themeGray(axisContrast))
	return img
}

// gradient colors by height like btop, brighter further from the axis.
func gradient(dark, bright color.RGBA, t float64) color.RGBA {
	t = min(1, max(0, 0.3+0.7*t))
	mix := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t) }
	return color.RGBA{mix(dark.R, bright.R), mix(dark.G, bright.G), mix(dark.B, bright.B), 255}
}

// themeGray returns the gray that is contrast lighter (L*, 0-100) than the
// theme background, or darker when the foreground is darker.
func themeGray(contrast float64) color.Gray {
	bg := lightness(theme.Color(theme.ColorNameBackground))
	if lightness(theme.Color(theme.ColorNameForeground)) < bg {
		contrast = -contrast
	}
	return grayOf(min(100, max(0, bg+contrast)))
}

// lightness returns the CIE L* of a color.
func lightness(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	y := 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
	if y > 216.0/24389 {
		return 116*math.Cbrt(y) - 16
	}
	return y * 24389 / 27
}

func linear(v uint32) float64 {
	c := float64(v) / 0xffff
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// grayOf returns the sRGB gray with the given L*.
func grayOf(l float64) color.Gray {
	y := l * 27 / 24389
	if l > 8 {
		y = math.Pow((l+16)/116, 3)
	}
	c := 12.92 * y
	if y > 0.0031308 {
		c = 1.055*math.Pow(y, 1/2.4) - 0.055
	}
	return color.Gray{uint8(math.Round(c * 255))}
}
