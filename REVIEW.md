# Code Review: wayland Go Library

## BUG: FD double-close in multi-handler event dispatch

**Severity: Medium** | **File:** `proxy.go:103-118`

When multiple handlers are registered for the same event that carries file descriptors, `dispatchEvent` calls `r.Clone()` for each handler. Each clone shares the same underlying `fds` slice but has its own `fdIdx`. After each handler runs, unconsumed FDs in that handler's clone are closed via `syscall.Close`.

If handler 1 consumes only a subset of FDs, the remaining FDs get closed. When handler 2 runs with its own clone (starting `fdIdx=0`), it will read FD numbers that have already been closed by handler 1 -- handler 2 gets invalid/stale FDs.

**Scenario:** Event carries 2 FDs. Handler 1 reads only fd[0] (via backward compatibility or intentional). `UnconsumedFDs()` returns fd[1], which gets closed. Handler 2, starting from scratch via Clone, reads fd[0] (valid) and fd[1] (now a closed/invalid FD).

**Fix:** Move FD cleanup after ALL handlers complete. Track the maximum `fdIdx` across all clones and close from that point, or simply close unconsumed FDs from the original reader after the handler loop:

```go
func (p *Proxy) dispatchEvent(opcode uint16, r *wire.Reader) {
    p.eventsMu.Lock()
    handlers := make([]func(*wire.Reader), len(p.events[opcode]))
    copy(handlers, p.events[opcode])
    p.eventsMu.Unlock()

    for _, h := range handlers {
        cr := r.Clone()
        h(cr)
    }
    // Close unconsumed FDs once, from original reader
    for _, fd := range r.UnconsumedFDs() {
        _ = syscall.Close(fd)
    }
}
```

---

## BUG: Zombie map has no cleanup, leaks memory

**Severity: Low** | **File:** `conn.go:81-95`, `eventloop.go:82-101`

When an object is deleted (via `UnregisterProxy`), its per-opcode FD counts are stored in `c.zombies[objID]`. After the zombie FDs are consumed in `dispatch`, the map entry is never removed. For long-lived connections where many objects are created and destroyed, the `zombies` map grows indefinitely.

**Fix:** Delete the zombie entry after dispatching its FDs in `eventloop.go:88-96`:

```go
if isZombie {
    n := zombieFdCounts[opcode]
    if n > 0 {
        fds := c.wc.TakeFDs(n)
        for _, fd := range fds {
            _ = syscall.Close(fd)
        }
    }
    c.objectsMu.Lock()
    delete(c.zombies, objID)
    c.objectsMu.Unlock()
}
```

---

## BUG: OOB buffer limits messages to at most 4 file descriptors

**Severity: Low** | **File:** `wire/connection.go:117`

The ancillary data buffer is hardcoded to `syscall.CmsgSpace(4*28)`, which can only hold 4 file descriptors per `recvmsg` call. If a Wayland message carries more than 4 FDs, `MSG_CTRUNC` is set and `ReceiveMessage` returns an error ("control message truncated").

While most Wayland messages carry 0-2 FDs, some protocols (e.g., `wl_drm` or custom extensions) could legitimately send more. libwayland typically uses a buffer sized for 28 FDs.

**Fix:** Increase the buffer or use a loop that re-reads when `MSG_CTRUNC` is detected. For example:

```go
oob := make([]byte, syscall.CmsgSpace(28*4)) // 28 FDs, matching libwayland
```

---

## ISSUE: Event handler silently swallows unmarshal errors

**Severity: Low** | **File:** `cmd/wayland-scanner/templates.go:134-141`

Generated `On{Name}` handler wrappers silently drop unmarshal errors:

```go
func (o *{{$.TypeName}}) On{{.Name}}(fn {{.FuncName}}) {
    o.proxy.RegisterEvent({{.OpName}}, func(r *wire.Reader) {
        var ev {{.StructName}}
        if err := ev.Unmarshal(r); err != nil {
            return  // error silently dropped
        }
        fn(ev)
    })
}
```

If a compositor sends a malformed event, the callback is simply not called with no indication of an error. This makes debugging protocol issues nearly impossible.

**Fix:** Either log the error or expose a connection-level error callback for deserialization failures. At minimum, use the connection's logger:

```go
if err := ev.Unmarshal(r); err != nil {
    // log or propagate
    return
}
```

---

## ISSUE: `Flush()` is a no-op

**Severity: Low** | **File:** `eventloop.go:78-80`

```go
func (c *Conn) Flush() error {
    return nil
}
```

While `SendMessage` writes directly to the Unix socket (which is unbuffered), the `wl_display_flush` Wayland API call is documented to flush buffered data. If a buffering layer is ever added to the write path, this silently becomes broken.

**Fix:** Either add documentation explaining why it's a no-op, or implement actual flush by calling `c.wc.Flush()` if the wire connection supports buffering.

---

## ISSUE: `Roundtrip` uses a busy-wait pattern

**Severity: Low** | **File:** `display.go:168-179`

The `Roundtrip` loop contains a `default:` branch in its select that does nothing, creating an implicit busy-wait:

```go
for {
    select {
    case <-done:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    default:
    }
    if err := d.Dispatch(ctx); err != nil {
        return err
    }
}
```

When neither `done` nor `ctx.Done()` is ready and `Dispatch` returns immediately (e.g., no events pending), this loops without blocking. While `Dispatch` normally blocks, the pattern is fragile.

**Fix:** Use the `done` channel as the primary wait and run `Dispatch` in a loop without the `default`:

```go
for {
    if err := d.Dispatch(ctx); err != nil {
        return err
    }
    select {
    case <-done:
        return nil
    default:
    }
}
```

---

## ISSUE: `newConn` always uses `slog.Default()`

**Severity: Low** | **File:** `conn.go:36`

```go
logger: slog.Default(),
```

If the application hasn't set up structured logging, `slog.Default()` writes to stderr. For libraries, using a discard logger by default (or nil with nil-guards) is more appropriate.

**Fix:** Consider `slog.New(slog.NewTextHandler(io.Discard, nil))` or set `logger` to nil and add nil checks before logging calls.

---

## ISSUE: Scanner validation doesn't check for duplicate names

**Severity: Low** | **File:** `cmd/wayland-scanner/parse.go:37-68`

The `validate` function checks that names are non-empty, but doesn't detect duplicate interface/request/event/arg/enum names. Duplicates could cause silent overwrites during code generation (e.g., two requests with the same name producing only one generated method).

**Fix:** Add `map[string]bool` checks for uniqueness of names within each scope.

---

## Summary

| # | Severity | File | Description |
|---|----------|------|-------------|
| 1 | Medium | `proxy.go:109-118` | FD double-close in multi-handler dispatch: unconsumed FDs from handler 1 are closed before handler 2 reads them |
| 2 | Low | `conn.go:90`, `eventloop.go:88` | Zombie map entries never removed -- unbounded memory growth |
| 3 | Low | `wire/connection.go:117` | OOB buffer limited to 4 FDs; messages with more FDs fail with MSG_CTRUNC |
| 4 | Low | `cmd/wayland-scanner/templates.go:137` | Unmarshal errors in generated event handlers silently swallowed |
| 5 | Low | `eventloop.go:78` | `Flush()` is a no-op; misleading if buffering is added |
| 6 | Low | `display.go:169` | `Roundtrip` busy-wait pattern with `default:` branch |
| 7 | Low | `conn.go:36` | `slog.Default()` writes to stderr by default; consider discard logger |
| 8 | Low | `cmd/wayland-scanner/parse.go:37` | No duplicate name validation in scanner |

### What's Good

- The wire protocol (reader/writer) implementation is clean and well-tested with good round-trip coverage.
- Test suite is comprehensive: event dispatch, FD transport, concurrent send/dispatch, zombie FD handling, multi-handler dispatch, context cancellation, drain-on-close.
- Code generation generates compilable, correct Go with proper `new_id` type resolution, version guards, and cross-protocol binding support.
- Connection lifecycle management (close idempotency, drain, cleanup) is handled correctly.
- `-race` flag passes cleanly on all tests.
