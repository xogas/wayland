# wayland

Pure-Go Wayland client library for linux/amd64. Standard library only: no CGO.

## Install

Requires Go 1.27 or newer and a running Wayland compositor.

```sh
go get github.com/xogas/wayland
```

## Usage

Connect to the compositor, list the advertised globals, and bind one:

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/xogas/wayland"
)

type registryHandler struct{ globals *[]wayland.Global }

func (h registryHandler) Global(g wayland.Global) { *h.globals = append(*h.globals, g) }
func (h registryHandler) GlobalRemove(uint32)     {}

func main() {
    d, err := wayland.Connect()
    if err != nil {
        panic(err) // set WAYLAND_DISPLAY (with XDG_RUNTIME_DIR) or WAYLAND_SOCKET
    }
    defer d.Close()

    var globals []wayland.Global
    d.GetRegistry(registryHandler{globals: &globals})

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := d.Roundtrip(ctx); err != nil {
        panic(err)
    }

    for _, g := range globals {
        fmt.Println(g.Interface, g.Version)
    }
}
```

Globals are bound through generated helpers or the generic `Registry.Bind`:

```go
compositor, err := wayland.BindCompositor(reg, compositorGlobal)
```

Events are delivered through handlers you pass at bind time; see
[examples/](./examples/) for complete programs.

## Examples

| Example | Shows |
| --- | --- |
| [shm-surface](./examples/shm-surface) | minimal wl_shm window |
| [xdg-shell](./examples/xdg-shell) | xdg_toplevel window lifecycle |
| [registry](./examples/registry) / [output](./examples/output) / [input](./examples/input) | globals, outputs, seat events |
| [cube](./examples/cube) | frame-callback driven animation |
| [imageviewer](./examples/imageviewer) | `go run ./examples/imageviewer [path]` |
| [subsurface](./examples/subsurface) | synchronized wl_subsurface |
| [clipboard](./examples/clipboard) | wl_data_device copy/paste |
| [cursor](./examples/cursor) | cursor-shape-v1 with shm fallback |
| [dmabuf](./examples/dmabuf) | linux-dmabuf from a DRM dumb buffer |

## License

MIT - see [LICENSE](./LICENSE) for details.
