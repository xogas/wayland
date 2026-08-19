package main

import (
	"math"
)

// vec3 is a 3D vector in world space.
type vec3 [3]float64

// cubeVerts are the eight corners of a unit cube centered on the origin.
var cubeVerts = [8]vec3{
	{-1, -1, -1}, {1, -1, -1}, {1, 1, -1}, {-1, 1, -1},
	{-1, -1, 1}, {1, -1, 1}, {1, 1, 1}, {-1, 1, 1},
}

// faceDef is one cube face: its four vertex indices and its color.
type faceDef struct {
	idx   [4]int
	color [4]byte
}

// Face colors are in BGR byte order (XRGB8888 pixel layout).
var cubeFaces = [6]faceDef{
	{[4]int{1, 0, 3, 2}, [4]byte{0x00, 0x00, 0xFF, 0xFF}}, // front, red
	{[4]int{4, 5, 6, 7}, [4]byte{0x00, 0xFF, 0x00, 0xFF}}, // back, green
	{[4]int{3, 0, 4, 7}, [4]byte{0xFF, 0x00, 0x00, 0xFF}}, // left, blue
	{[4]int{5, 1, 2, 6}, [4]byte{0xFF, 0xFF, 0x00, 0xFF}}, // right, yellow
	{[4]int{7, 6, 2, 3}, [4]byte{0x00, 0xFF, 0xFF, 0xFF}}, // top, cyan
	{[4]int{0, 1, 5, 4}, [4]byte{0xFF, 0x00, 0xFF, 0xFF}}, // bottom, magenta
}

func rotY(v vec3, a float64) vec3 {
	s, c := math.Sincos(a)
	return vec3{v[0]*c + v[2]*s, v[1], -v[0]*s + v[2]*c}
}

func rotX(v vec3, a float64) vec3 {
	s, c := math.Sincos(a)
	return vec3{v[0], v[1]*c - v[2]*s, v[1]*s + v[2]*c}
}

func sub(a, b vec3) vec3 { return vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }

func cross(a, b vec3) vec3 {
	return vec3{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

// edge returns the signed edge function for the half-plane test.
func edge(ax, ay, bx, by, px, py float64) float64 {
	return (px-ax)*(by-ay) - (py-ay)*(bx-ax)
}

// rasterTriangle fills a triangle with the scanline half-plane algorithm.
func rasterTriangle(data []byte, s int, x0, y0, x1, y1, x2, y2 float64, c [4]byte) {
	minX := max(0, int(min(x0, min(x1, x2))))
	maxX := min(winW-1, int(max(x0, max(x1, x2))))
	minY := max(0, int(min(y0, min(y1, y2))))
	maxY := min(winH-1, int(max(y0, max(y1, y2))))
	for py := minY; py <= maxY; py++ {
		off := py * s
		for px := minX; px <= maxX; px++ {
			e0 := edge(x0, y0, x1, y1, float64(px), float64(py))
			e1 := edge(x1, y1, x2, y2, float64(px), float64(py))
			e2 := edge(x2, y2, x0, y0, float64(px), float64(py))
			if (e0 >= 0 && e1 >= 0 && e2 >= 0) || (e0 <= 0 && e1 <= 0 && e2 <= 0) {
				o := off + px*4
				data[o+0] = c[0]
				data[o+1] = c[1]
				data[o+2] = c[2]
				data[o+3] = c[3]
			}
		}
	}
}

// clearBlack wipes the buffer to opaque black.
func clearBlack(data []byte) {
	for i := range len(data) {
		data[i] = 0
	}
}

// renderCube rotates the cube by (ay, ax) and draws the visible faces.
func renderCube(data []byte, ay, ax float64) {
	// Rotate, then translate the cube along -Z so it sits in front of the
	// camera.
	var rv [8]vec3
	for i, v := range cubeVerts {
		p := rotX(rotY(v, ay), ax)
		p[2] -= cubeDist
		rv[i] = p
	}

	// Keep the faces that point at the camera, projected to screen space.
	type faceInfo struct {
		col     [4]byte
		depth   float64
		screenV [4][2]float64
	}
	var visible []faceInfo
	for _, f := range cubeFaces {
		v0, v1, v2 := rv[f.idx[0]], rv[f.idx[1]], rv[f.idx[2]]
		n := cross(sub(v1, v0), sub(v2, v0))
		if n[2] <= 0 {
			continue // backface
		}
		fi := faceInfo{col: f.color}
		for j, vi := range f.idx {
			p := rv[vi]
			nz := -p[2]
			fi.screenV[j] = [2]float64{focal*p[0]/nz + winW/2, winH/2 - focal*p[1]/nz}
			fi.depth += p[2]
		}
		fi.depth /= 4
		visible = append(visible, fi)
	}

	// Painter's algorithm: draw the farthest faces first.
	for i := 0; i < len(visible); i++ {
		for j := i + 1; j < len(visible); j++ {
			if visible[i].depth > visible[j].depth {
				visible[i], visible[j] = visible[j], visible[i]
			}
		}
	}
	for _, f := range visible {
		sv := f.screenV
		rasterTriangle(data, stride, sv[0][0], sv[0][1], sv[1][0], sv[1][1], sv[2][0], sv[2][1], f.col)
		rasterTriangle(data, stride, sv[0][0], sv[0][1], sv[2][0], sv[2][1], sv[3][0], sv[3][1], f.col)
	}
}
