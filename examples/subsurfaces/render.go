package main

import (
	"math"
)

// drawGradient renders the static gradient background of the main surface.
func drawGradient(data []byte, w, h, stride int) {
	for y := range h {
		rowOff := y * stride
		for x := range w {
			off := rowOff + x*4
			data[off+0] = uint8(float64(x) / float64(w) * 96)
			data[off+1] = uint8(float64(y) / float64(h) * 128)
			data[off+2] = uint8(40 + float64(x)/float64(w)*64 + float64(y)/float64(h)*40)
			data[off+3] = 0xff
		}
	}
}

// drawSub renders a pulsing, hue-cycling disk into the sub-surface buffer.
func drawSub(data []byte, stride, frame int) {
	cx := float64(subW) * 0.5
	cy := float64(subH) * 0.5
	t := float64(frame) * 0.06

	for y := range subH {
		rowOff := y * stride
		dy := float64(y) - cy
		for x := range subW {
			dx := float64(x) - cx
			d := math.Sqrt(dx*dx + dy*dy)
			rMax := 40.0 + 20.0*math.Sin(t*1.3)
			off := rowOff + x*4
			if d < rMax {
				hue := math.Atan2(dy, dx) + t
				r := uint8((math.Sin(hue) + 1) * 0.5 * 255)
				g := uint8((math.Sin(hue+2.094) + 1) * 0.5 * 255)
				b := uint8((math.Sin(hue+4.189) + 1) * 0.5 * 255)
				fade := 1.0 - d/rMax
				data[off+0] = uint8(float64(b) * fade)
				data[off+1] = uint8(float64(g) * fade)
				data[off+2] = uint8(float64(r) * fade)
				data[off+3] = 0xff
			} else {
				data[off+0] = 0
				data[off+1] = 0
				data[off+2] = 0
				data[off+3] = 0
			}
		}
	}
}

// subPosition returns the sub-surface origin for the given frame: it orbits
// the main surface on a circular path.
func subPosition(frame int) (int, int) {
	radius := float64((mainW-subW)/2 - 20)
	t := float64(frame) * 0.04
	px := int(float64(mainW)*0.5 - float64(subW)*0.5 + radius*math.Cos(t))
	py := int(float64(mainH)*0.5 - float64(subH)*0.5 + radius*math.Sin(t))
	return px, py
}
