// Package wayland is a pure Go Wayland client library for linux/amd64.
//
// It uses the standard library only: no CGO, no libwayland, no
// wayland-scanner. Protocol bindings are generated from wayland-protocols/
// by cmd/wayland-gen.
//
// Files:
//
//	arg.go        wire argument constructors and accessors
//	close.go      Display.Close, socket shutdown, fd cleanup
//	conn.go       connection state, dispatch guard
//	connect.go    Connect, Dial, socket resolution
//	dispatch.go   Dispatch, frame dispatch, event delivery
//	errors.go     error sentinels, ProtocolError
//	interface.go  interface metadata, InterfaceByName
//	io.go         read/write loop, frame IO, fd queue
//	object.go     object table, client IDs, delete_id
//	proxy.go      Proxy, EventSink, Global
//	roundtrip.go  Roundtrip, cancellation, tombstone
//	wire.go       request encoding, event decoding
//
// Generated files:
//
//	wayland_gen.go     core wl_* bindings from wayland.xml
//	interfaces_gen.go  metadata for all interfaces
//	protocols/         per-XML protocol packages
//
// Tools and examples:
//
//	cmd/wayland-gen  XML -> Go bindings
//	examples/       standalone programs: registry, output, input, and
//	                shm-surface, xdg-shell, cube, imageviewer, subsurface,
//	                clipboard, cursor, dmabuf
package wayland
