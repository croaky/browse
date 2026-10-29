package main

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"io"
	"math"
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
//
// The palette is the standard library's Plan 9 palette, with no
// dithering. A page is mostly flat color, and dithering adds noise
// that changes from frame to frame, which makes the file larger and
// the playback flicker.
func writeGIF(w io.Writer, frames []frame, end float64) error {
	if len(frames) == 0 {
		return errors.New("no frames")
	}
	canvas, err := jpeg.DecodeConfig(bytes.NewReader(frames[len(frames)-1].JPEG))
	if err != nil {
		return err
	}
	var lut colorIndex
	out := &gif.GIF{Config: image.Config{
		ColorModel: color.Palette(palette.Plan9),
		Width:      canvas.Width,
		Height:     canvas.Height,
	}}
	for i, f := range frames {
		src, err := jpeg.Decode(bytes.NewReader(f.JPEG))
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
		out.Image = append(out.Image, lut.paletted(src, canvas.Width, canvas.Height))
		out.Delay = append(out.Delay, delay)
	}
	return gif.EncodeAll(w, out)
}

// colorIndex maps a color to its nearest Plan 9 palette entry. It
// keeps 5 bits of each channel as the key and fills an entry on first
// use. Without it, each pixel of each frame searches all 256 entries.
type colorIndex struct {
	entries [1 << 15]uint8
	known   [1 << 15]bool
}

// paletted draws src on a w by h canvas. It scales src to the canvas
// width with the nearest pixel, and paints white below a src that is
// short.
func (c *colorIndex) paletted(src image.Image, w, h int) *image.Paletted {
	b := src.Bounds()
	white := uint8(color.Palette(palette.Plan9).Index(color.White))
	dst := image.NewPaletted(image.Rect(0, 0, w, h), palette.Plan9)
	for y := range h {
		sy := y * b.Dx() / w
		for x := range w {
			if sy >= b.Dy() {
				dst.SetColorIndex(x, y, white)
				continue
			}
			r, g, bl, _ := src.At(b.Min.X+x*b.Dx()/w, b.Min.Y+sy).RGBA()
			key := (r>>11)<<10 | (g>>11)<<5 | bl>>11
			if !c.known[key] {
				c.entries[key] = uint8(color.Palette(palette.Plan9).Index(color.RGBA64{R: uint16(r), G: uint16(g), B: uint16(bl), A: 0xffff}))
				c.known[key] = true
			}
			dst.SetColorIndex(x, y, c.entries[key])
		}
	}
	return dst
}
