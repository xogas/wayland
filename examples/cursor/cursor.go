package main

import (
	"github.com/xogas/wayland"
	"github.com/xogas/wayland/protocol/staging/cursorshape"
	"github.com/xogas/wayland/wire"
)

const (
	modeCustom = 1
	modeShape  = 2

	cursorSize int32 = 32
	hotspot    int32 = 16
)

// shapeCycle is the cursor-shape-v1 shape list cycled by the arrow keys.
var shapeCycle = []cursorshape.CursorShapeDeviceV1Shape{
	cursorshape.CursorShapeDeviceV1ShapeDefault,
	cursorshape.CursorShapeDeviceV1ShapePointer,
	cursorshape.CursorShapeDeviceV1ShapeCrosshair,
	cursorshape.CursorShapeDeviceV1ShapeText,
	cursorshape.CursorShapeDeviceV1ShapeMove,
	cursorshape.CursorShapeDeviceV1ShapeGrab,
}

// shapeNames mirrors shapeCycle for printing.
var shapeNames = []string{
	"default",
	"pointer",
	"crosshair",
	"text",
	"move",
	"grab",
}

// app holds the cursor-related state shared by the input handlers.
type app struct {
	pointer        *wayland.Pointer
	cursorSurface  *wayland.Surface
	csDevice       *cursorshape.CursorShapeDeviceV1
	hasCursorShape bool
	mode           int
	lastSerial     uint32
	shapeIdx       int
}

// applyCursor (re)applies the cursor according to the current mode.
func (a *app) applyCursor() {
	switch a.mode {
	case modeCustom:
		_ = a.pointer.SetCursor(a.lastSerial, wire.ObjectID(a.cursorSurface.Proxy().ID()), hotspot, hotspot)
	case modeShape:
		if a.csDevice != nil {
			_ = a.csDevice.SetShape(a.lastSerial, shapeCycle[a.shapeIdx])
		}
	}
}

// drawCrosshair renders the self-drawn crosshair cursor: a 32x32 ARGB
// surface, transparent except for a cross through the hotspot.
func drawCrosshair(data []byte, stride int32) {
	clearBuffer(data)
	for x := range cursorSize {
		off := int(hotspot*stride + x*4)
		data[off+0] = 0xFF
		data[off+1] = 0xFF
		data[off+2] = 0xFF
		data[off+3] = 0xFF
	}
	for y := range cursorSize {
		off := int(y*stride + hotspot*4)
		data[off+0] = 0xFF
		data[off+1] = 0xFF
		data[off+2] = 0xFF
		data[off+3] = 0xFF
	}
}

// clearBuffer clears the buffer (transparent black).
func clearBuffer(data []byte) {
	for i := range len(data) {
		data[i] = 0
	}
}
