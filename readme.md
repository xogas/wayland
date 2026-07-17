# wayland

A Wayland client protocol library in pure Go. Zero dependencies beyond the standard library.

- wayland version: 1.25.0
- wayland-protocols version: 1.49

## Features

- Complete wire protocol implementation: Unix socket I/O, `SCM_RIGHTS` fd passing
- Core protocol (wayland.xml) bindings in the root `wayland` package — a single import
- 65+ extension protocol bindings (xdg-shell, viewporter, presentation-time, and more), organized by stability tier under `protocol/`
- Code generator `wayland-scanner`: produces type-safe Go bindings from protocol XML
- Concurrency-safe event dispatch with proper object and fd lifecycle management
- Generated comments: enum values, struct fields, and types carry the XML summaries inline

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

More runnable examples in [example/](./example/readme.md): window creation, shm rendering, input handling, sub-surfaces, and more.

## Code generation

`*_gen.go` files are produced by `wayland-scanner`. Do not edit them by hand:

```sh
make gen
```

## Requirements

- Linux
- Go 1.26+
- A running Wayland compositor (KWin, Mutter, Sway, Weston, etc.)

## Packages at a glance

| Package | Content |
| :--- | :--- |
| `github.com/xogas/wayland` | Connection management, core protocol bindings, runtime engine |
| `github.com/xogas/wayland/wire` | Low-level message encoding, fd control, reader/writer |
| `github.com/xogas/wayland/protocol/stable/*` | Stable extension protocols (xdg-shell, viewporter, linux-dmabuf, ...) |
| `github.com/xogas/wayland/protocol/staging/*` | Staging protocols (fractional-scale, cursor-shape, tearing-control, ...) |
| `github.com/xogas/wayland/protocol/unstable/*` | Unstable protocols (pointer-constraints, relative-pointer, text-input, ...) |
| `github.com/xogas/wayland/protocol/experimental/*` | Experimental protocols |

## License

MIT - see [license](./license) for details.
