package wayland

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/xogas/wayland/wire"
)

const displayID uint32 = 1

// Connect establishes a Wayland display connection.
func Connect(ctx context.Context) (*Display, error) {
	path, fd, err := resolveSocket()
	if err != nil {
		return nil, err
	}
	if fd >= 0 {
		return ConnectFd(ctx, fd, path)
	}
	return ConnectPath(ctx, path)
}

// ConnectPath dials the Wayland display socket at the given path.
func ConnectPath(ctx context.Context, path string) (*Display, error) {
	var d net.Dialer
	uc, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("wayland: dial %s: %w", path, err)
	}
	unixConn, ok := uc.(*net.UnixConn)
	if !ok {
		_ = uc.Close()
		return nil, fmt.Errorf("wayland: expected *net.UnixConn, got %T", uc)
	}
	wc := wire.NewConn(unixConn)
	conn := newConn(unixConn, wc)
	proxy := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(proxy)
	dpy := NewDisplay(proxy)
	wireDisplayEvents(dpy, conn)
	return dpy, nil
}

// ConnectFd uses an already-opened file descriptor to create the display connection.
func ConnectFd(ctx context.Context, fd int, path string) (*Display, error) {
	f := os.NewFile(uintptr(fd), path)
	defer f.Close() //nolint: errcheck
	uc, err := net.FileConn(f)
	if err != nil {
		return nil, fmt.Errorf("wayland: FileConn from fd %d: %w", fd, err)
	}
	unixConn, ok := uc.(*net.UnixConn)
	if !ok {
		_ = uc.Close()
		return nil, fmt.Errorf("wayland: expected *net.UnixConn from fd, got %T", uc)
	}
	_ = os.Unsetenv("WAYLAND_SOCKET")
	wc := wire.NewConn(unixConn)
	conn := newConn(unixConn, wc)
	proxy := NewProxyWithID(conn, displayID)
	conn.RegisterProxy(proxy)
	dpy := NewDisplay(proxy)
	wireDisplayEvents(dpy, conn)
	return dpy, nil
}

func wireDisplayEvents(dpy *Display, conn *Conn) {
	dpy.OnError(func(ev DisplayErrorEvent) {
		conn.connMu.Lock()
		fn := conn.onError
		conn.connMu.Unlock()
		if fn == nil {
			return
		}
		pe := &ProtocolError{
			ObjectID: uint32(ev.ObjectID),
			Code:     ev.Code,
			Message:  ev.Message,
		}
		fn(pe)
	})
	dpy.OnDeleteID(func(ev DisplayDeleteIDEvent) {
		conn.UnregisterProxy(ev.ID)
	})
}

func resolveSocket() (path string, fd int, err error) {
	if s := os.Getenv("WAYLAND_SOCKET"); s != "" {
		v, parseErr := strconv.Atoi(s)
		if parseErr != nil {
			return "", -1, fmt.Errorf("wayland: WAYLAND_SOCKET=%q is not an integer", s)
		}
		return "", v, nil
	}
	path = os.Getenv("WAYLAND_DISPLAY")
	if path == "" {
		runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
		if runtimeDir == "" {
			return "", -1, fmt.Errorf("wayland: XDG_RUNTIME_DIR not set and WAYLAND_DISPLAY not set")
		}
		return runtimeDir + "/wayland-0", -1, nil
	}
	if strings.HasPrefix(path, "/") {
		return path, -1, nil
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", -1, fmt.Errorf("wayland: XDG_RUNTIME_DIR not set, needed for relative WAYLAND_DISPLAY=%q", path)
	}
	return runtimeDir + "/" + path, -1, nil
}

// SetOnError registers a callback for protocol error events.
func (d *Display) SetOnError(fn func(*ProtocolError)) {
	conn := d.Proxy().Conn()
	conn.connMu.Lock()
	conn.onError = fn
	conn.connMu.Unlock()
}

// SetLogger sets the structured logger for the display connection.
func (d *Display) SetLogger(l *slog.Logger) {
	d.Proxy().Conn().SetLogger(l)
}

// Close closes the display connection.
func (d *Display) Close() error {
	return d.Proxy().Conn().Close()
}

// Conn returns the underlying connection.
func (d *Display) Conn() *Conn {
	return d.Proxy().Conn()
}

// Dispatch blocks until a single event is received and dispatched.
func (d *Display) Dispatch(ctx context.Context) error {
	return d.Proxy().Conn().Dispatch(ctx)
}

// DispatchPending dispatches all pending events without blocking.
func (d *Display) DispatchPending() error {
	return d.Proxy().Conn().DispatchPending()
}

// Flush sends any buffered data to the compositor.
func (d *Display) Flush() error {
	return d.Proxy().Conn().Flush()
}

// Roundtrip sends a sync request and blocks until the server has processed it
// and all preceding requests, dispatching events along the way.
func (d *Display) Roundtrip(ctx context.Context) error {
	cb, err := d.Sync()
	if err != nil {
		return err
	}
	done := make(chan struct{})
	cb.OnDone(func(ev CallbackDoneEvent) {
		close(done)
	})
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
}
