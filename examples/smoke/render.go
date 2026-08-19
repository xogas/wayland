package main

// render scales the low-resolution density field up to the window and maps
// it to a warm smoke color.
func render(data []byte, sim *Sim) {
	for y := range winH {
		sy := y / simScale
		for x := range winW {
			sx := x / simScale
			d := sim.dens[simIX(sx, sy)]
			r := byte(clamp(d*400, 0, 255))
			g := byte(clamp(d*180, 0, 255))
			b := byte(clamp(d*80, 0, 255))
			off := y*stride + x*4
			data[off+0] = b
			data[off+1] = g
			data[off+2] = r
			data[off+3] = 0xff
		}
	}
}
