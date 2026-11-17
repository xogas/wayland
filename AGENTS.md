# wayland development conventions

## Scope

Pure-Go Wayland client library and XML -> Go binding generator for linux/amd64.
It targets the Wayland protocol, not a specific compositor.

## Hard constraints (violation = revert)

- Go 1.27, per the `go` directive in `go.mod`; standard library only; linux/amd64;
  no CGO, libwayland, or third-party Go modules.
- `wayland-protocols/` is the official upstream input. Update it by replacing the
  directory from upstream; never hand-edit its XML or DTD.
- `wayland_gen.go`, `interfaces_gen.go`, and `protocols/**` are generated. Never
  hand-edit them; change the XML or `cmd/wayland-gen` and run `make generate`.
- `make generate` owns cleanup: `rm -rf protocols`, `rm -f ./*_gen.go`, then
  `go run ./cmd/wayland-gen`. Do not use `go generate` or add `generate.go`.
- Public API is stable. Ask before changing exported signatures, semantics, or
  error identities. Keep the `Proxy()` / generated `New` seam and error sentinels.
- Comments stay high-signal: why, protocol, concurrency, fd ownership, boundaries.
  Do not restate names. Exported APIs get one line. `doc.go` is the file map;
  README stays minimal.
- Git: one logical change per commit. Stage explicit paths only; no `git add -A`
  or `git commit -am`. Sign commits. Do not push, tag, amend pushed commits, or
  force-push.
- If uncertain, stop and ask. Precedence: current instruction > this file.

## Workflow

1. State scope: what changes, what does not, and the affected files.
2. Ask before public API, generated layout, protocol set, tag, or `go.mod` changes.
3. Make the smallest change. No drive-by refactors, dependency upgrades, or next tasks.
4. Stop when done; report unverified items.

## Verification

1. File list and why each changed.
2. Raw output, not a summary:
   - `make generate` twice; `git diff --exit-code -- wayland_gen.go interfaces_gen.go protocols`
   - `make test`
   - `go test -race -count=1 -tags=integration ./...`
   - `go vet ./...`; `test -z "$(gofmt -l .)"`; `golangci-lint run ./...`
   - examples registry / output / shm-surface
3. Deviations from this file: list each, or write none.
4. xdg-shell / input Esc and pointer checks are manual; state if not run.

## Pointers

- Package and file layout: `doc.go`
- Commands: `README.md`, `Makefile`
- Generator: `cmd/wayland-gen`
- Protocol XML: `wayland-protocols/`
- Lint: `.golangci.yml`
- Uncovered: ask

## Commits

- English Conventional Commits: `type: subject`; types: feat, fix, docs, style,
  refactor, perf, test, build, ci, chore, revert.
- Subject: imperative, lowercase start, no trailing period, 72 chars max.
- No body: the message is the subject line only.
- One logical change per commit; sign.
- Do not push, tag, amend pushed commits, or force-push.
