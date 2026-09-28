package main

import (
	"image"
	"image/color"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	// historySize is the number of samples kept per adapter, one per second
	historySize = 3600
	// sampleWidth is the width of a sample in the graph
	sampleWidth = 2
	// markerPeriod is the number of seconds between the time markers
	markerPeriod = 10
)

// gridColor is used for the bandwidth lines and the time markers.
var gridColor = color.NRGBA{128, 128, 128, 48}

// history holds the latest rates of an adapter in bytes per second, oldest
// first.
type history struct {
	down, up []float64
	// count is the number of samples ever added, it keeps the time markers
	// in place when old samples are dropped
	count int
}

func (h *history) add(down, up float64) {
	if len(h.down) == historySize {
		h.down, h.up = h.down[1:], h.up[1:]
	}
	h.down = append(h.down, down)
	h.up = append(h.up, up)
	h.count++
}

// graph draws the history of an adapter like btop does: download above the
// axis and upload below it. Both halves have the same height and each uses
// its own scale, a half stays empty when its scale is unknown.
type graph struct {
	widget.BaseWidget
	raster             *canvas.Raster
	downText, upText   *widget.Label
	samples            *history
	scaleDown, scaleUp float64 // bits per second
}

func newGraph() *graph {
	g := &graph{}
	g.raster = canvas.NewRaster(g.draw)
	g.downText = widget.NewLabel("")
	g.downText.SizeName = theme.SizeNameCaptionText
	g.upText = widget.NewLabel("")
	g.upText.SizeName = theme.SizeNameCaptionText
	g.ExtendBaseWidget(g)
	return g
}

func (g *graph) CreateRenderer() fyne.WidgetRenderer {
	labels := container.NewBorder(g.downText, g.upText, nil, nil)
	return widget.NewSimpleRenderer(container.NewStack(g.raster, labels))
}

func (g *graph) MinSize() fyne.Size {
	return fyne.NewSize(100, 120)
}

func (g *graph) set(samples *history, scaleDown, scaleUp float64) {
	g.samples, g.scaleDown, g.scaleUp = samples, scaleDown, scaleUp
	g.downText.SetText("Down " + formatScale(scaleDown, scaleDown > 0))
	g.upText.SetText("Up " + formatScale(scaleUp, scaleUp > 0))
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
	// lines at 25, 50, 75 and 100% of the download and upload scale
	for q := 1; q <= 4; q++ {
		down := axis - int(math.Round(float64(axis)*float64(q)/4))
		up := axis + int(math.Round(float64(h-axis)*float64(q)/4))
		fill(img, image.Rect(0, down, w, down+1), gridColor)
		fill(img, image.Rect(0, min(up, h-1), w, min(up, h-1)+1), gridColor)
	}
	// time markers move left with the samples
	for i := 0; w-i*col > 0; i++ {
		if (g.samples.count-1-i)%markerPeriod == 0 {
			x := w - i*col - 1
			fill(img, image.Rect(x, 0, x+1, h), gridColor)
		}
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
	fill(img, image.Rect(0, axis, w, axis+1), frameBorder)
	return img
}

// gradient colors by height like btop, brighter further from the axis.
func gradient(dark, bright color.RGBA, t float64) color.RGBA {
	t = min(1, max(0, 0.3+0.7*t))
	mix := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t) }
	return color.RGBA{mix(dark.R, bright.R), mix(dark.G, bright.G), mix(dark.B, bright.B), 255}
}
