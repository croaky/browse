package main

import (
	"bytes"
	"cmp"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"maps"
	"math"
	"slices"
)

// writeGIF encodes the frames as an animated GIF that plays at real
// speed. Each frame shows until the next frame's timestamp, and the
// last frame shows until end. A frame that does not decode is an
// error.
//
// The canvas is the size of the last frame, which finish captures at
// the size of the PNG. Screencast frames can differ: a headful Chrome
// sends frames without its window bars, and a device scale can differ.
// So each frame scales to the canvas width, keeps its aspect, and pads
// the rest of the canvas white.
func writeGIF(w io.Writer, frames []frame, end float64) error {
	if len(frames) == 0 {
		return errors.New("no frames")
	}
	canvas, err := png.DecodeConfig(bytes.NewReader(frames[len(frames)-1].PNG))
	if err != nil {
		return err
	}
	// No global palette: each frame carries its own.
	out := &gif.GIF{Config: image.Config{Width: canvas.Width, Height: canvas.Height}}
	for i, f := range frames {
		src, err := png.Decode(bytes.NewReader(f.PNG))
		if err != nil {
			return err
		}
		next := end
		if i+1 < len(frames) {
			next = frames[i+1].At
		}
		// GIF counts delay in hundredths of a second. Browsers show a
		// delay below 2 as 10, so 2 is the shortest delay to write.
		delay := max(2, int(math.Round((next-f.At)*100)))
		out.Image = append(out.Image, paletted(src, canvas.Width, canvas.Height))
		out.Delay = append(out.Delay, delay)
	}
	return gif.EncodeAll(w, out)
}

// paletted draws src on a w by h canvas. It scales src to the canvas
// width with the nearest pixel, and paints white below a src that is
// short.
//
// The palette is the 256 colors the frame uses most, with no
// dithering. A page is mostly flat color, so its background, text,
// and borders keep their exact colors. Each other color, such as an
// edge pixel of anti-aliased text, takes the nearest palette color. A
// fixed palette such as Plan 9 holds few of a page's colors, and it
// puts a colored halo around text. A gradient or a photo shows bands.
func paletted(src image.Image, w, h int) *image.Paletted {
	b := src.Bounds()
	px := make([]color.RGBA, w*h)
	count := map[color.RGBA]int{}
	for y := range h {
		sy := y * b.Dx() / w
		for x := range w {
			c := color.RGBA{0xff, 0xff, 0xff, 0xff}
			if sy < b.Dy() {
				r, g, bl, _ := src.At(b.Min.X+x*b.Dx()/w, b.Min.Y+sy).RGBA()
				c = color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), 0xff}
			}
			px[y*w+x] = c
			count[c]++
		}
	}
	used := slices.SortedFunc(maps.Keys(count), func(a, b color.RGBA) int {
		return cmp.Compare(count[b], count[a])
	})
	pal := make(color.Palette, 0, 256)
	for _, c := range used[:min(256, len(used))] {
		pal = append(pal, c)
	}
	// Each color searches the palette once, not once for each pixel.
	index := map[color.RGBA]uint8{}
	dst := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	for i, c := range px {
		k, ok := index[c]
		if !ok {
			k = uint8(pal.Index(c))
			index[c] = k
		}
		dst.Pix[i] = k
	}
	return dst
}
