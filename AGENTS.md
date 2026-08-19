# AGENTS.md

Pure-Go Wayland client protocol library. Zero dependencies, stdlib only.
wayland core 1.26.0, wayland-protocols 1.49 (vendored in `wayland-protocols/`, not a submodule), Go 1.26+.

## Layout

| Path | Purpose |
| --- | --- |
| `conn.go`, `display.go`, `eventloop.go`, `proxy.go`, `errors.go` | Core runtime: connection, event loop, proxies, errors |
| `*_gen.go` (root) | Generated wayland core bindings, DO NOT EDIT |
| `wire/` | Message codec, SCM_RIGHTS fd queue |
| `protocol/` | Generated extension bindings, tiered `stable/` `staging/` `unstable/` `experimental/` |
| `cmd/wayland-scanner/` | Code generator (`protocol.go`, `naming.go`, `templates.go`, `transform.go`) |
| `cmd/wayland-info/` | Protocol info tool |
| `examples/` | Runnable examples |
| `wayland-protocols/` | Protocol XML sources (vendored) |

## Golden rule: generated code

- All `*_gen.go` produced by `cmd/wayland-scanner`; never hand-edit
- To change protocol behavior, edit `cmd/wayland-scanner/`, then `make gen`; output must be deterministic (byte-identical for same XML)
- All protocol elements (incl. deprecated) generated equally; deprecated elements carry standard `// Deprecated:` annotations (with since-version), check XML for semantics
- New protocol: drop XML into `wayland-protocols/` tier, run `make gen`

## Design

- Dedicated reader goroutine reads socket; `Dispatch` / `DispatchPending` run handlers on calling goroutine; single-goroutine dispatch, never call `Dispatch` / `Roundtrip` inside a handler
- Failures are fatal and sticky: protocol errors (`*ProtocolError`), undecodable events, unknown-object/opcode events kill the connection; afterwards every call fails fast with the same error
- Unknown-opcode/object events are fatal because their fd count is unknowable; skipping would throw the fd queue out of sync
- Events for client-destroyed objects are dropped (fds drained and closed); normal, not an error
- Requests written immediately, no send buffering, `Flush` is a no-op; `SendRequest` is goroutine-safe, dispatch is single-goroutine
- Detect protocol errors with `errors.As(err, &wayland.ProtocolError{})`; `SetOnError` is a pre-close hook

## Naming

- XML snake_case -> PascalCase, `id` -> `ID`; interface names drop `wl_` / `zwp_` prefixes
- Requests `XxxRequest`, events `XxxEvent`, interfaces `Surface`, `Registry`, ...
- Constants `InterfaceXxx`, `VersionXxx`, opcodes `XxxRequestYyy` (uint16)
- Request/event types implement `Opcode() uint16`, `Marshal(*wire.Writer) error`, `Since() uint32`
- Interfaces provide `NewXxx(p *Proxy) *Xxx` and `BindXxx(b Binder, name, version) (*Xxx, error)`

## Build & test

```sh
make gen        # regenerate *_gen.go
make build      # build wayland-scanner / wayland-info into bin/
make test       # go test -race ./...
make test-soak  # stress test (TestSoak)
make vet        # go vet ./...
```

- `integration_test.go` / `soak_test.go` need a running compositor (KWin, Mutter, Sway, Weston)
- After touching core runtime, keep generated-code interface compatible, run `make test`

## Principles

- Zero third-party deps, stdlib only
- No compatibility promise within 1.x: both API shape (call sites may need updating) and behavior (error values, edge cases, timing) may change with bug fixes
- Protocol support only grows; new interfaces/requests/events as wayland-protocols evolves
- Conventional commits: `feat:` `fix:` `chore:` `refactor:` `test:` `docs:` `style:`
