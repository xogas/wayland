# wayland

A Wayland client protocol library in pure Go. Zero dependencies beyond the stdlib.

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
  and `DispatchPending` run event handlers on the calling goroutine. Dispatch
  from a single goroutine (usually your main loop) and never from inside an
  event handler.
- **Failures are fatal**: a `wl_display.error`, an undecodable event, or a
  stream violation (an event for a never-created object, or an opcode the
  bound interface lacks) kills the connection. `Dispatch` returns the error
  (a `*ProtocolError` for compositor-reported ones), and every later call
  fails fast with the same sticky error. Check with
  `errors.As(err, &wayland.ProtocolError{})`; `SetOnError` fires before close.
  Unknown-opcode/object events are fatal because their fd count is unknowable:
  skipping them would desync the fd queue.
- **Destroy race**: events in flight for a client-destroyed object are dropped
  (their fds drained and closed). Normal, not an error.
- **No send buffering**: requests are written to the socket immediately, so
  `Flush` is a no-op. `SendRequest` is goroutine-safe; dispatch is
  single-goroutine.

## Code generation

`*_gen.go` files are produced by `wayland-scanner`; do not edit them by hand:

```sh
make gen
```

Deprecated elements are generated and handled like current ones, so no code
changes are needed to keep using them; their identifiers carry standard
`// Deprecated:` annotations (with the since-version) so staticcheck flags
their use. Consult the protocol XML for the deprecation semantics.

## Stability

No API or behavioral compatibility promise within 1.x: call sites may need
updating, and bug fixes may change error values, edge cases, or timing.
Protocol support only grows; `make gen` output is deterministic (byte-identical
for the same XML inputs).

## License

MIT - see [license](./license) for details.
