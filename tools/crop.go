// crop.go — enlarge a rectangular region of a rendered page for verbatim
// proofreading of dense text.
//
//   go run tools/crop.go in.png out.png x0 y0 x1 y1 [scale]
//
// Coordinates are pixels of the source image; scale is an integer (default 2).
package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) < 7 {
		fmt.Fprintln(os.Stderr, "usage: go run tools/crop.go in.png out.png x0 y0 x1 y1 [scale]")
		os.Exit(2)
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		panic(err)
	}
	b := img.Bounds()
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	x0, y0, x1, y1 := atoi(os.Args[3]), atoi(os.Args[4]), atoi(os.Args[5]), atoi(os.Args[6])
	scale := 2
	if len(os.Args) > 7 {
		scale = atoi(os.Args[7])
	}
	if scale < 1 {
		scale = 1
	}
	if x1 > b.Dx() { x1 = b.Dx() }
	if y1 > b.Dy() { y1 = b.Dy() }
	if x0 < 0 { x0 = 0 }
	if y0 < 0 { y0 = 0 }
	if x1 <= x0 || y1 <= y0 {
		panic("empty crop rect")
	}
	rect := image.Rect(x0, y0, x1, y1)
	sub := img.(interface{ SubImage(r image.Rectangle) image.Image }).SubImage(rect)
	w, h := rect.Dx()*scale, rect.Dy()*scale
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.Set(x, y, sub.At(x/scale+rect.Min.X, y/scale+rect.Min.Y))
		}
	}
	o, err := os.Create(os.Args[2])
	if err != nil {
		panic(err)
	}
	defer o.Close()
	if err = png.Encode(o, out); err != nil {
		panic(err)
	}
	fmt.Printf("%s %dx%d\n", os.Args[2], w, h)
}
