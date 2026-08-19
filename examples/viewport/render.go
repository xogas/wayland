package main

// drawBuffer renders the checkerboard pattern at physical resolution: each
// logical pixel of the 512x512 design becomes a scale x scale block.
func drawBuffer(data []byte, stride int, scale int32) {
	size := logBufSize * scale
	for y := 0; y < int(size); y++ {
		for x := 0; x < int(size); x++ {
			lx := x / int(scale)
			ly := y / int(scale)
			cx := lx / 64
			cy := ly / 64
			var r, g, b uint8
			if (cx+cy)&1 == 0 {
				r = uint8((cx * 37) % 256)
				g = uint8((cy * 53) % 256)
				b = uint8(((cx + cy) * 23) % 256)
			} else {
				r = uint8(((7 - cx) * 37) % 256)
				g = uint8(((7 - cy) * 53) % 256)
				b = uint8(((14 - cx - cy) * 23) % 256)
			}
			t := float64(lx+ly) / float64(logBufSize*2-2)
			r = uint8(float64(r)*(1-t) + 255*t)
			g = uint8(float64(g)*(1-t) + 140*t)
			b = uint8(float64(b)*(1-t) + 60*t)
			off := y*stride + x*4
			data[off+0] = b
			data[off+1] = g
			data[off+2] = r
			data[off+3] = 0xff
		}
	}
}
