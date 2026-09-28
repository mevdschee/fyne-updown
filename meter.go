package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"

	"fyne.io/fyne/v2"
)

// The tray icon mimics the one of UpDown Meter: a dark frame with two meters,
// drawn at twice the original 16x16 size for high DPI trays. Unlike UpDown
// Meter the download meter is on the top half and the upload meter on the
// bottom half, like in the graph. The upload is red and the download green,
// as uploads are the ones to watch.

const (
	iconSize   = 32
	meterSteps = 14
)

var (
	frameBorder = color.RGBA{128, 128, 128, 255}
	frameFill   = color.RGBA{80, 80, 80, 255}
	upBright    = color.RGBA{220, 0, 0, 255}
	upDark      = color.RGBA{70, 0, 0, 255}
	downBright  = color.RGBA{0, 200, 0, 255}
	downDark    = color.RGBA{0, 70, 0, 255}
)

var meterIcons = map[[2]int]fyne.Resource{}

// meterIcon returns the tray icon for the given up and down fractions (0-1),
// the icons are quantized and cached, as trays may store every icon they get.
func meterIcon(up, down float64) fyne.Resource {
	key := [2]int{quantize(up), quantize(down)}
	if res, ok := meterIcons[key]; ok {
		return res
	}
	img := drawMeter(key[0], key[1])
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	res := fyne.NewStaticResource(fmt.Sprintf("meter-%d-%d.png", key[0], key[1]), buf.Bytes())
	meterIcons[key] = res
	return res
}

func quantize(f float64) int {
	if math.IsNaN(f) || f <= 0 {
		return 0
	}
	// show a sliver for any traffic
	return min(meterSteps, max(1, int(math.Round(f*meterSteps))))
}

func drawMeter(up, down int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, iconSize, iconSize))
	fill(img, image.Rect(0, 0, iconSize, iconSize), color.Black)
	fill(img, image.Rect(0, 0, iconSize-2, iconSize-2), frameBorder)
	fill(img, image.Rect(2, 2, iconSize-2, iconSize-2), frameFill)
	// central divider
	fill(img, image.Rect(2, 14, iconSize-2, 16), frameBorder)
	fill(img, image.Rect(2, 16, iconSize-2, 18), color.Black)
	drawBar(img, 2, down, downBright, downDark)
	drawBar(img, 18, up, upBright, upDark)
	return img
}

// drawBar draws a striped bar of 12 pixels high, starting at row y.
func drawBar(img *image.RGBA, y, steps int, bright, dark color.RGBA) {
	width := steps * (iconSize - 4) / meterSteps
	fill(img, image.Rect(2, y, 2+width, y+12), bright)
	for x := 2 + 4; x < 2+width; x += 6 {
		fill(img, image.Rect(x, y, min(x+2, 2+width), y+12), dark)
	}
}

func fill(img *image.RGBA, r image.Rectangle, c color.Color) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.Set(x, y, c)
		}
	}
}
