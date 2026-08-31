# Wayland Go Binding Examples

Standalone Wayland client examples demonstrating the github.com/xogas/wayland library.

All examples except helloworld (self-contained minimal starting point) share [internal/shared](./internal/shared):
display connection, global discovery, toplevel window creation (auto-responds to configure), shm double buffering, dispatch loop, and drawing utilities.
This package is example-only, not part of the public API.

Structure across examples:

- `run()` in main.go handles connection, binding, and main loop; errors propagate to `main()` for printing
- Larger examples split by responsibility (e.g. smoke: sim.go for solver, render.go for rendering)
- Input handlers record state or enqueue; protocol operations run in the main loop

## Requirements

- Linux with a running Wayland compositor (Weston, Sway, KWin, Mutter, etc.)
- `XDG_RUNTIME_DIR` set
- Go 1.27+

## Basic Examples

### globals -- Global Interface Enumeration

Lists all global interfaces with their names and version numbers.

```sh
go run ./examples/globals
```

### outputs -- Monitor Info and Hotplug Listener

Binds all `wl_output` globals, prints name/resolution/refresh/scale for each, then monitors hotplug events for 5 seconds.

```sh
go run ./examples/outputs
```

### helloworld -- Minimal Window with Embedded Font

Creates a toplevel window via xdg-shell, renders "Hello, Wayland!" with an embedded 8x8 bitmap font. Window size auto-fits text.

```sh
go run ./examples/helloworld
```

No input; exits after 30 seconds.

## Rendering Examples

### simpleshm -- Double-Buffered Animation

250x250 window with evolving concentric rings.
Demonstrates double buffering (2 shm buffers + `wl_buffer` release tracking) and `wl_surface.frame`-driven animation.

```sh
go run ./examples/simpleshm
```

No input; exits after 60 seconds, prints frame-rate stats.

### damage -- Incremental Damage Demo

400x400 window, square orbiting a circle. Uses `wl_surface.damage` or `damage_buffer` to refresh only dirty regions; reports damage area ratio.

```sh
go run ./examples/damage
```

D toggles incremental/full damage (~0.007 vs 1.0 ratio).

### viewport -- wp_viewporter Crop/Scale + HiDPI

512x512 logical buffer with a 256x256 source rectangle via wp_viewport; target size adjustable.
Demonstrates visible region changes without re-attaching the buffer, `wl_shm.format` negotiation, and buffer-scale HiDPI rendering with optional `wp_fractional_scale.preferred_scale`.

```sh
go run ./examples/viewport
```

Space pause/resume; -/= shrink/enlarge; S toggle 1x/2x buffer scale.

### presentation -- presentation-time Feedback

256x256 window with a moving square. Each frame reports commit-to-present latency (avg/min/max) via `wp_presentation_feedback`.

```sh
go run ./examples/presentation
```

No input; prints latency report every 60 frames.

### cube -- Software-Rendered 3D Cube

480x480 window, pure CPU rendering: perspective projection, back-face culling, painter's algorithm, scanline rasterization.

```sh
go run ./examples/cube
```

No input; exits after 60 seconds, prints frame rate.

## Input Examples

### eventdemo -- Unified Input Event Viewer

640x260 window with full ASCII 32-126 font. Prints all `wl_keyboard`, `wl_pointer`, and `wl_touch` events in real time.
Full-buffer redraw on each event (simple, not optimized).

```sh
go run ./examples/eventdemo
```

No input; exits after 60 seconds.

### cursor -- Cursor Shape Demo

400x300 window with two cursor modes: (A) self-drawn crosshair surface; (B) compositor-native cursor via cursor-shape-v1.

```sh
go run ./examples/cursor
```

1: mode A; 2: mode B. In mode B, left/right arrows cycle shapes: default, pointer, crosshair, text, move, grab.

### smoke -- Smoke Fluid Simulation

400x400 window, Jos Stam fluid solver. Mouse movement stirs smoke; inactivity triggers periodic random injection.

```sh
go run ./examples/smoke
```

No click required; exits after 60 seconds.

## Window Management Examples

### resizor -- Interactive Window Management

Drag to move, edge-drag to resize, keyboard shortcuts for maximize/fullscreen/minimize/manual-resize.
Colored borders indicate state. Optional server-side decoration via zxdg_decoration_manager_v1.

```sh
go run ./examples/resizor
```

m maximize; f fullscreen; n minimize; up/down adjust height (30px steps); left-drag move; edge-drag resize; q quit. Exits after 120 seconds.

### popup -- Right-Click Menu with xdg_positioner

400x300 window, right-click opens a self-drawn 160x100 menu (Item 1/2/3). Auto-popup at 2s, auto-close at 3s.
Demonstrates xdg_popup + xdg_positioner + grab.

```sh
go run ./examples/popup
```

Right-click menu; left-click select; auto-popup demo at 2s.

### subsurfaces -- Subsurface Animation

400x400 main window + 120x120 subsurface orbiting a circle. Demonstrates sync/desync switching and z-order control.

```sh
go run ./examples/subsurfaces
```

S toggle sync/desync; R toggle place_above/place_below.

### activation -- xdg-activation Focus Transfer

Two 300x200 windows (red, blue). Tab transfers focus via xdg_activation_v1.

```sh
go run ./examples/activation
```

Tab transfers focus; auto-activates window B after 3s idle.

## Data Exchange Examples

### dnd -- Drag-and-Drop and Clipboard

500x300 window with 4 colored squares. Left-drag triggers drag-and-drop (`application/x-color`); c/v copy/paste via `wl_data_device`.

```sh
go run ./examples/dnd
```

Left-drag to move; c copy; v paste. Exits after 120 seconds.

## Media Examples

### imageviewer -- Image Viewer

Decodes PNG/JPEG/GIF via stdlib and displays in a window; images above 1600x1000 are scaled down proportionally.

```sh
go run ./examples/imageviewer <image-file>
```

No input; exits after 60 seconds.
