# wayland

A Wayland client protocol library in pure Go. Zero dependencies beyond the standard library.

- wayland version: 1.26.0
- wayland-protocols version: 1.49

## Requirements

- Linux
- Go 1.26+
- A running Wayland compositor (KWin, Mutter, Sway, Weston, etc.)

## Install

```sh
go get github.com/xogas/wayland
```

## Quick start

Connect to the compositor and list all available globals:

```go
package main

import (
    "context"
    "fmt"

    "github.com/xogas/wayland"
)

func main() {
    ctx := context.Background()

    dpy, err := wayland.Connect(ctx)
    if err != nil {
        panic(err)
    }
    defer dpy.Close()

    var globals []wayland.RegistryGlobalEvent
    reg, _ := dpy.GetRegistry()
    reg.OnGlobal(func(ev wayland.RegistryGlobalEvent) {
        globals = append(globals, ev)
    })

    dpy.Roundtrip(ctx)
    for _, g := range globals {
        fmt.Printf("%s (version %d)\n", g.Interface, g.Version)
    }
}
```

More runnable examples in [example/](./example/readme.md).

## Design notes

- **Event model**: a dedicated reader goroutine reads the socket; `Dispatch`
  and `DispatchPending` run your event handlers on the calling goroutine.
  Call `Dispatch` from a single goroutine (typically your main loop), and do
  not call `Dispatch` or `Roundtrip` from inside an event handler.
- **Failures are fatal**: a `wl_display.error` event, an event that cannot be
  decoded from the wire, or a stream-level violation (an event for an object
  that never existed, or an opcode the bound interface does not define)
  terminates the connection. `Dispatch` returns the error — a
  `*ProtocolError` for compositor-reported errors — and every subsequent
  call fails fast with the same sticky error. Detect protocol errors with
  `errors.As(err, &wayland.ProtocolError{})`; `SetOnError` is an optional
  notification hook that fires before the connection closes. Unknown-opcode
  and unknown-object events are fatal because their fd count is unknowable:
  skipping them could desynchronize the connection-level fd queue.
- **Destroy race**: events already in flight for a client-destroyed object are
  dropped (any fds they carry are drained and closed). This is normal
  protocol operation, not an error.
- **No send buffering**: each request is written to the socket immediately, so
  `Flush` is a no-op. Requests are serialized internally, so `SendRequest` is
  safe from multiple goroutines; dispatch itself is single-goroutine.

## Code generation

`*_gen.go` files are produced by `wayland-scanner`. Do not edit them by hand:

```sh
make gen
```

Deprecated protocol elements are generated and handled identically to
current ones, so no code changes are needed to keep using them. Their generated
identifiers carry standard `// Deprecated:` Go annotations (with the since-version
note), so tooling like staticcheck flags their use; consult the protocol XML for
the deprecation semantics.

## Stability

Within the 1.x line, the API shape is stable: existing call sites keep
compiling, and protocol support only grows — new interfaces, requests, and
events are added as wayland-protocols evolves, and regenerating with
`make gen` is deterministic (byte-identical output for the same XML inputs).

Behavior is explicitly not part of the guarantee: bug fixes may change error
values, edge-case handling, or timing between releases, so treat observed
runtime behavior in any 1.x release as an implementation detail.

Releases before v1.0.0 (the 0.0.x tags) are snapshots of the API as it
matures and carry no compatibility guarantee.

## License

MIT - see [license](./license) for details.
