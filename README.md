# wayland

Pure-Go Wayland client library plus a generator that turns the protocol XML in
this repository into Go bindings. It targets small Linux desktop tools that talk
to sway.

- Status: v0.1.0
- Platform: linux/amd64. The wire protocol uses host byte order; amd64 is
  little-endian.
- Dependencies: standard library only. No CGO, no libwayland, no wayland-scanner.
- Protocol input: `wayland-protocols/` with 68 XML files and `wayland.dtd`;
  wayland 1.26.0 and wayland-protocols 1.50.
- Generated code is committed. `go generate ./...` is deterministic and must
  leave the generated paths unchanged after a second run.

## Repository layout

### Runtime, package `wayland`

| File | Responsibility |
| :--- | :--- |
| `arg.go` | wire argument constructors and accessors |
| `close.go` | `Display.Close`, socket shutdown, fd cleanup |
| `conn.go` | connection state, dispatch guard, `testHooks` pointer |
| `connect.go` | `Connect`, `DialContext`, `DialFD`, source resolution |
| `dispatch.go` | `Dispatch`, frame dispatch, `wl_display` events, sink delivery |
| `errors.go` | error sentinels and `ProtocolError` |
| `interface.go` | interface metadata types and `InterfaceByName` / `MustInterface` |
| `io.go` | read/write loops, frame IO, fd queue, soft read timeout |
| `object.go` | object table, client ID allocation, delete_id, destroy state |
| `proxy.go` | `Proxy` handle, `EventSink`, `Global` |
| `roundtrip.go` | `Roundtrip`, cancellation, callback tombstone, unread frame |
| `testhooks.go` | test-only raw-bind and unknown-event skip hooks; nil in production |
| `wire.go` | request encoding, event decoding, dynamic `wl_registry.bind` fields |
| `protocol_gen.go` | generated core `wl_*` bindings |
| `protocol_meta_gen.go` | generated metadata for all interfaces |

### Generator, `cmd/waylandgen`

Single `package main`; no subpackages.

| File | Responsibility |
| :--- | :--- |
| `main.go` | flags, pipeline, atomic output |
| `parse.go` | XML decoding |
| `model.go` | protocol model and construction |
| `validate.go` | semantic validation and collision rules |
| `names.go` | prefix stripping, type/package names |
| `render.go` | render orchestration and output files |
| `render_types.go` | types, handlers, enums, request/event methods |
| `render_meta.go` | metadata literals and root metadata table |
| `render_binds.go` | bind helpers and the `wl_registry.bind` special case |
| `render_names.go` | Go type/expression mapping for arguments and events |
| `*_test.go`, `testdata/` | generator AC tests and fixtures |

### Other paths

| Path | Responsibility |
| :--- | :--- |
| `protocols/<tier>/<protocol>/<protocol>_gen.go` | generated bindings for non-core protocols |
| `examples/{registry,shm-surface,xdg-shell,input,output}/main.go` | standalone examples |
| root `*_test.go`, `integration_test.go` | runtime unit, race, and live-sway tests |
| `scripts/check-dep.sh` | dependency-direction and generated-header check |
| `wayland-protocols/` | protocol XML and DTD source |
| `Makefile` | test / vet / clean |
| `.golangci.yml` | lint configuration; generated files excluded |

## Generated code

`go generate ./...` runs `cmd/waylandgen` and writes:

- `protocol_gen.go`: core `wl_*` types in the root package.
- `protocol_meta_gen.go`: metadata for all 211 interfaces, including the
  stable-preference map and all duplicate entries.
- `protocols/<tier>/<protocol>/<protocol>_gen.go`: typed bindings for the
  remaining protocols.

Do not edit generated files by hand. Change the generator and regenerate.

## Public API

- `wayland.Connect`, `wayland.DialContext`, `wayland.DialFD`.
- `(*wayland.Display).Sync`, `GetRegistry`, `Dispatch`, `Roundtrip`, `Close`.
- `(*wayland.Registry).Bind` plus generated `Bind<T>` helpers.
- Generated protocol types and handler interfaces under `protocols/...`.
- `wayland.InterfaceByName`, `wayland.MustInterface`.
- `wayland.Proxy`, `wayland.Arg`, `wayland.EventSink`, `wayland.Global`.
- Generated types expose `Proxy()` and `New<T>` (or `NewDisplay`) as a seam
  for typed callbacks and cross-package object arguments.

## Examples

Each example is a standalone `main`. Run from the repository root in a live
sway session:

```bash
go run ./examples/registry
go run ./examples/shm-surface
go run ./examples/xdg-shell
go run ./examples/input
go run ./examples/output
```

Required environment:

```bash
export XDG_RUNTIME_DIR=/run/user/1000
export WAYLAND_DISPLAY=wayland-1
```

`WAYLAND_SOCKET` can be used instead when the caller already owns a connected
socket fd. Integration tests use `-tags=integration` and the same session.

## Limitations

- Public typed bindings cover the 68 XML files. Unknown sway globals (wlroots /
  KDE) have no public binding; the registry-bind tests use an unexported
  test-only raw bind.
- `xdg-shell` and `input` need a human to press Esc and move the pointer for the
  final manual check; automated runs verify startup, mapping, and close.
- `ArgSpec.Since` and `ArgSpec.DeprecatedSince` exist but are always 0 because
  the current DTD has no arg-level `since` attribute.
- `EnumEntry.Value` is `int64` to hold both int32 and uint32 ranges.
- xdg-shell type names follow prefix stripping (`Surface`, `Toplevel`), not the
  illustrative `XdgSurface` spelling.
- Request enum parameters use generated enum types; event enum parameters use
  the underlying `int32` / `uint32`.
- A partial write of an fd-bearing message sends the ancillary batch once;
  continuation writes do not repeat it, so each fd arrives exactly once.
- Duplicate interface losers keep local metadata for their own `new_id` but do
  not generate a `Bind<T>` helper; the stable winner does.
- Protocols not advertised by sway are generated and wire-tested, but are not
  claimed to work against a live compositor.

## Verification

```bash
go build ./...
CGO_ENABLED=0 go build ./...
go vet ./...
test -z "$(gofmt -l .)"
golangci-lint run ./...
go test -race -count=1 ./...
go test -race -count=1 -tags=integration ./...
go mod tidy && git diff --exit-code -- go.mod go.sum
./scripts/check-dep.sh
```

Generation and protocol validation:

```bash
go generate ./...
go generate ./...
git diff --exit-code
for f in $(find wayland-protocols -name '*.xml'); do
  xmllint --noout --dtdvalid wayland-protocols/wayland.dtd "$f" || exit 1
done
```

## License

MIT. See [License](./License).
