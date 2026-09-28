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

// The tray icon is based on the one of UpDown Meter, drawn at twice the
// original 16x16 size for high DPI trays. Unlike UpDown Meter it is flat and
// the meters stand upright: two gray halves with a transparent gap between
// them, download on the left and upload on the right. The upload is red and
// the download green, as uploads are the ones to watch.

const (
	iconSize   = 32
	meterSteps = 14
)

var (
	meterBackground = color.RGBA{80, 80, 80, 255}
	upBright        = color.RGBA{220, 0, 0, 255}
	upDark          = color.RGBA{70, 0, 0, 255}
	downBright      = color.RGBA{0, 200, 0, 255}
	downDark        = color.RGBA{0, 70, 0, 255}
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
	drawBar(img, 0, down, downBright)
	drawBar(img, iconSize/2+1, up, upBright)
	return img
}

// drawBar draws a meter of 15 pixels wide from column x, the bar grows
// upwards and has a margin of two pixels.
func drawBar(img *image.RGBA, x, steps int, c color.RGBA) {
	fill(img, image.Rect(x, 0, x+iconSize/2-1, iconSize), meterBackground)
	top := iconSize - 2 - steps*(iconSize-4)/meterSteps
	fill(img, image.Rect(x+2, top, x+iconSize/2-3, iconSize-2), c)
}

func fill(img *image.RGBA, r image.Rectangle, c color.Color) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.Set(x, y, c)
		}
	}
}
