# wayland

A pure-Go Wayland client library plus a generator that builds Go bindings from
the protocol XML checked into this repository. The library is meant for small
Linux desktop tools such as screenshots and launchers that talk to sway.

- **Status**: v0.1.0
- **Platform**: linux/amd64 only. The wire protocol is encoded in host byte
  order; this repository assumes little-endian amd64.
- **Dependencies**: zero third-party Go modules, standard library only. CGO is
  not used and libwayland is neither linked nor required at runtime.
- **Protocol input**: all 68 XML files under `wayland-protocols/`, validated
  against `wayland-protocols/wayland.dtd`.
- **Bundled protocol versions**: wayland 1.26.0, wayland-protocols 1.50.

## Generated code

`go generate ./...` runs `cmd/waylandgen` and regenerates:

- `protocol_gen.go`: generated core `wl_*` bindings in the module root.
- `protocol_meta_gen.go`: generated metadata for all 211 interfaces
  (198 unique wire names).
- `protocols/<tier>/<protocol>/<protocol>_gen.go`: generated bindings for the
  remaining protocols.

Generated files are committed deliverables. Do not edit them by hand; change
the generator and regenerate. Generation is deterministic: two consecutive runs
leave the generated-path diff at 0
(`git diff --exit-code -- protocol_gen.go protocol_meta_gen.go protocols`).

## Public API entry points

- `wayland.Connect`, `wayland.DialContext`, `wayland.DialFD`: open a connection
  using `WAYLAND_SOCKET`, `WAYLAND_DISPLAY` + `XDG_RUNTIME_DIR`, or an explicit
  socket path / fd.
- `(*wayland.Display).Sync`, `GetRegistry`, `Dispatch`, `Roundtrip`, `Close`.
- `(*wayland.Registry).Bind` plus generated `Bind<T>` helpers for every unique
  generated interface.
- Generated protocol types and handler interfaces in the module root and under
  `protocols/...`, for example `wayland.Compositor` and
  `protocols/stable/xdg-shell.BindWmBase`.
- Metadata helpers `wayland.InterfaceByName` and `wayland.MustInterface`.

Runtime concurrency rules and error identifiers are frozen in
`docs/contracts.md`; usage examples are under `examples/`.

## Examples

Each example is a standalone program. Run them from the repository root in a
live sway session:

```bash
go run ./examples/registry
go run ./examples/shm-surface
go run ./examples/xdg-shell
go run ./examples/input
go run ./examples/output
```

Required Wayland environment:

```bash
export XDG_RUNTIME_DIR=/run/user/1000
export WAYLAND_DISPLAY=wayland-1
```

`WAYLAND_SOCKET` can be used instead when the caller already owns a connected
socket fd. Integration tests use `-tags=integration` and require the same live
sway session.

## Known limitations

- Unknown globals advertised by sway (wlroots / KDE interfaces outside the 68
  XML files) have no public typed binding. `Registry.Bind` rejects them. The
  registry-bind test covers them through a test-only unexported raw bind helper;
  that helper is not public API and does not expand the supported protocol set.
- AC-X03 (xdg-shell) and AC-X04 (input) are manual pending: automated runs
  verify startup, mapping, and clean close, but the final Esc / pointer-coordinate
  check needs a human on an empty workspace. This is accepted at S-04 G4; the
  project does not claim those two ACs as fully PASS.
- `ArgSpec.Since` and `ArgSpec.DeprecatedSince` exist in the metadata type but
  are always 0 because the current Wayland DTD has no arg-level `since`
  attribute. The generator does not invent a source for them.
- The 13 duplicate interface losers (same wire name in different protocol
  tiers) keep local metadata for their own `new_id` use but do not generate a
  `Bind<T>` helper. Their stable-preference winner does.
- AC-W04 ancillary deviation: a partial write of an fd-bearing message sends the
  ancillary batch once. Continuation writes do not repeat the same ancillary
  data, so the peer receives each fd exactly once in order. This is the
  accepted interpretation of "retry the same ancillary" from the frozen
  contracts.
- Protocols not advertised by sway are generated and wire-tested but are not
  claimed to work against a live compositor.

## Verification

```bash
go build ./...
CGO_ENABLED=0 go build ./...
go vet ./...
test -z "$(gofmt -l .)"
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

Registry-bind acceptance:

```bash
go test -race -tags=integration -run TestRegistryBind -v ./...
go test -race -tags=integration -run TestRegistryBindUnknownEvents -v ./...
go test -count=1 -run TestRegistryBindError -v ./...
```

## License

MIT - see [License](./License).
