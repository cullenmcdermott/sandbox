package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/draw"
	_ "image/jpeg" // register JPEG decoder for image.Decode

	"github.com/cullenmcdermott/sandbox/tui/terminal"
)

// catJPEG is a real cat photo, transmitted as a Kitty graphics image when "show
// me a kitty image" is asked on a Kitty-capable terminal. It's the payoff for the
// whole tui/terminal Kitty path: a genuine RGBA bitmap over the APC _G protocol.
//
//go:embed cat.jpg
var catJPEG []byte

// catPhotoTransmission decodes the embedded cat photo to RGBA and builds the
// Kitty transmission that binds it to catID over a catCols×catRows placement.
// Returns "" if the image can't be decoded.
func catPhotoTransmission() string {
	rgba, w, h, ok := catPhotoRGBA()
	if !ok {
		return ""
	}
	return terminal.KittyTransmitRGBA(catID, catCols, catRows, w, h, rgba)
}

// catPhotoRGBA decodes the embedded JPEG into row-major, 8-bit RGBA (the format
// KittyTransmitRGBA wants: width*height*4 bytes).
func catPhotoRGBA() (rgba []byte, w, h int, ok bool) {
	img, _, err := image.Decode(bytes.NewReader(catJPEG))
	if err != nil {
		return nil, 0, 0, false
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst.Pix, b.Dx(), b.Dy(), true
}
