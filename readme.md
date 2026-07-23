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
