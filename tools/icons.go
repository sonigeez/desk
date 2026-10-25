//go:build ignore

// Draws the app icons in static/ from the three-spine mark in static/icon.svg.
// Run it from the repo root after changing the mark: go run tools/icons.go
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
)

type rect struct {
	x, y, w, h, r float64
	c             color.RGBA
}

var (
	paper = color.RGBA{0xf2, 0xf1, 0xec, 0xff}
	gilt  = color.RGBA{0xde, 0xc4, 0x8a, 0xff}
)

// The mark, in icon.svg's 32-unit space: three spines, then their gilt rules (0.8 wide).
var mark = func() []rect {
	m := []rect{
		{5, 20, 22, 7, 1.2, color.RGBA{0x6c, 0x2e, 0x2c, 0xff}},
		{3, 12.5, 23, 6.5, 1.2, color.RGBA{0x2d, 0x44, 0x60, 0xff}},
		{7, 5, 20, 6.5, 1.2, color.RGBA{0x55, 0x60, 0x3e, 0xff}},
	}
	for _, l := range [][3]float64{{8, 22, 25}, {10, 22, 25}, {6, 14.5, 17}, {8, 14.5, 17}, {10, 7, 9.5}, {12, 7, 9.5}} {
		m = append(m, rect{l[0] - 0.4, l[1], 0.8, l[2] - l[1], 0, gilt})
	}
	return m
}()

func (r rect) has(x, y float64) bool {
	if x < r.x || x > r.x+r.w || y < r.y || y > r.y+r.h {
		return false
	}
	cx := math.Max(r.x+r.r, math.Min(x, r.x+r.w-r.r))
	cy := math.Max(r.y+r.r, math.Min(y, r.y+r.h-r.r))
	return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r.r*r.r
}

// draw renders the mark on paper at size px, the mark's 24 units of width taking up the given share of it.
func draw(name string, size int, share float64) {
	const ss = 4 // samples per pixel side
	scale := share * float64(size) / 24
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var sr, sg, sb float64
			for j := 0; j < ss; j++ {
				for i := 0; i < ss; i++ {
					// back to mark space, centred on the mark's middle (15, 16)
					x := (float64(px)+(float64(i)+0.5)/ss-float64(size)/2)/scale + 15
					y := (float64(py)+(float64(j)+0.5)/ss-float64(size)/2)/scale + 16
					c := paper
					for _, r := range mark {
						if r.has(x, y) {
							c = r.c
						}
					}
					sr, sg, sb = sr+float64(c.R), sg+float64(c.G), sb+float64(c.B)
				}
			}
			n := float64(ss * ss)
			img.SetRGBA(px, py, color.RGBA{uint8(sr/n + 0.5), uint8(sg/n + 0.5), uint8(sb/n + 0.5), 0xff})
		}
	}
	f, err := os.Create("static/" + name)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
}

func main() {
	draw("icon-192.png", 192, 0.7)
	draw("icon-512.png", 512, 0.7)
	draw("apple-touch-icon.png", 180, 0.66)
	// Maskable icons get cropped to as little as the middle circle of 80%, so the mark stays well inside it.
	draw("icon-maskable-512.png", 512, 0.54)
}
