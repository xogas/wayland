# wayland

A Wayland client protocol library in pure Go. Zero dependencies beyond the standard library.

- wayland version: 1.25.0
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
- **Failures are fatal**: a `wl_display.error` event or an event that cannot be
  decoded from the wire terminates the connection. `Dispatch` returns the
  error — a `*ProtocolError` for compositor-reported errors — and every
  subsequent call fails fast with the same sticky error. Detect protocol
  errors with `errors.As(err, &wayland.ProtocolError{})`; `SetOnError` is an
  optional notification hook that fires before the connection closes.
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

No `// Deprecated:` Go annotations are emitted.
All protocol elements (including deprecated ones) are generated and handled identically.
Refer to the protocol protocol XML descriptions to determine deprecation status.

## License

MIT - see [license](./license) for details.
