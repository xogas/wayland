package main

import (
	"fmt"
	"slices"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/examples/internal/shared"
	"github.com/xogas/wayland/protocol/stable/xdgshell"
)

// stateName renders an xdg_toplevel state for the terminal output.
func stateName(s xdgshell.ToplevelState) string {
	switch s {
	case xdgshell.ToplevelStateMaximized:
		return "maximized"
	case xdgshell.ToplevelStateFullscreen:
		return "fullscreen"
	case xdgshell.ToplevelStateResizing:
		return "resizing"
	case xdgshell.ToplevelStateActivated:
		return "activated"
	case xdgshell.ToplevelStateTiledLeft:
		return "tiled_left"
	case xdgshell.ToplevelStateTiledRight:
		return "tiled_right"
	case xdgshell.ToplevelStateTiledTop:
		return "tiled_top"
	case xdgshell.ToplevelStateTiledBottom:
		return "tiled_bottom"
	case xdgshell.ToplevelStateSuspended:
		return "suspended"
	case xdgshell.ToplevelStateConstrainedLeft:
		return "constrained_left"
	case xdgshell.ToplevelStateConstrainedRight:
		return "constrained_right"
	case xdgshell.ToplevelStateConstrainedTop:
		return "constrained_top"
	case xdgshell.ToplevelStateConstrainedBottom:
		return "constrained_bottom"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

// hasState reports whether states contains target.
func hasState(states []xdgshell.ToplevelState, target xdgshell.ToplevelState) bool {
	return slices.Contains(states, target)
}

// diffStates splits the state change into added and removed entries.
func diffStates(old, new []xdgshell.ToplevelState) (added, removed []xdgshell.ToplevelState) {
	oldSet := make(map[xdgshell.ToplevelState]bool)
	newSet := make(map[xdgshell.ToplevelState]bool)
	for _, s := range old {
		oldSet[s] = true
	}
	for _, s := range new {
		newSet[s] = true
	}
	for _, s := range new {
		if !oldSet[s] {
			added = append(added, s)
		}
	}
	for _, s := range old {
		if !newSet[s] {
			removed = append(removed, s)
		}
	}
	return
}

// redraw renders the background, a state-colored border, the size label and
// the current toplevel states into a fresh buffer and commits it.
func redraw(t *shared.Toplevel, core *shared.Core, w, h int32, states []xdgshell.ToplevelState) error {
	bufID, data, cleanup, err := shared.NewBuffer(core.Shm, w, h, wayland.ShmFormatXrgb8888)
	if err != nil {
		return err
	}
	defer cleanup()

	stride := int(w) * 4
	shared.FillRect(data, stride, int(w), int(h), 0, 0, int(w), int(h), 0xDD, 0xCC, 0xCC)
	borderColor := [3]byte{0x99, 0x88, 0x88}
	if hasState(states, xdgshell.ToplevelStateActivated) {
		borderColor = [3]byte{0xCC, 0x88, 0x44}
	}
	if hasState(states, xdgshell.ToplevelStateResizing) {
		borderColor = [3]byte{0x44, 0x88, 0xCC}
	}
	const border = 4
	drawBorder := func(x, y, w, h int32) {
		shared.FillRect(data, stride, int(w), int(h), int(x), int(y), int(w), int(h),
			borderColor[0], borderColor[1], borderColor[2])
	}
	drawBorder(0, 0, w, border)
	drawBorder(0, h-border, w, border)
	drawBorder(0, 0, border, h)
	drawBorder(w-border, 0, border, h)

	const scale = 3
	textSize := fmt.Sprintf("%dx%d", w, h)
	textW := shared.TextWidth(textSize, scale)
	textH := shared.TextHeight(scale)
	centerX := (int(w) - textW) / 2
	centerY := (int(h) - textH) / 2
	shared.DrawText(data, stride, int(w), int(h), textSize, centerX, centerY, scale, 0x000000)

	stY := centerY + textH + 2*scale
	lineH := textH + scale
	for i, s := range states {
		label := stateName(s)
		lw := shared.TextWidth(label, scale)
		lx := (int(w) - lw) / 2
		ly := stY + i*lineH
		shared.DrawText(data, stride, int(w), int(h), label, lx, ly, scale, 0x000000)
	}

	_ = t.Surface.Attach(bufID, 0, 0)
	_ = t.Surface.Damage(0, 0, w, h)
	_ = t.Surface.Commit()
	return nil
}

// edgeFromCoords maps a pointer position inside the window to the resize
// edge to drag, with a 20px margin around the borders.
func edgeFromCoords(x, y, w, h int32) xdgshell.ToplevelResizeEdge {
	const margin int32 = 20
	if x < margin && y < margin {
		return xdgshell.ToplevelResizeEdgeTopLeft
	}
	if x >= w-margin && y < margin {
		return xdgshell.ToplevelResizeEdgeTopRight
	}
	if x < margin && y >= h-margin {
		return xdgshell.ToplevelResizeEdgeBottomLeft
	}
	if x >= w-margin && y >= h-margin {
		return xdgshell.ToplevelResizeEdgeBottomRight
	}
	if y < margin {
		return xdgshell.ToplevelResizeEdgeTop
	}
	if y >= h-margin {
		return xdgshell.ToplevelResizeEdgeBottom
	}
	if x < margin {
		return xdgshell.ToplevelResizeEdgeLeft
	}
	if x >= w-margin {
		return xdgshell.ToplevelResizeEdgeRight
	}
	return xdgshell.ToplevelResizeEdgeNone
}
