package wayland

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Connect opens a Wayland connection using WAYLAND_SOCKET, WAYLAND_DISPLAY
// and XDG_RUNTIME_DIR, in that order.
func Connect() (*Display, error) {
	path, fd, err := resolveSource()
	if err != nil {
		return nil, err
	}
	if fd >= 0 {
		return DialFD(fd)
	}
	return DialContext(context.Background(), path)
}

// DialContext opens an explicit Wayland socket path.
func DialContext(ctx context.Context, path string) (*Display, error) {
	if path == "" {
		return nil, fmt.Errorf("dial: empty path: %w", ErrSource)
	}
	d := net.Dialer{}
	raw, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w: %w", path, ErrConnect, err)
	}
	uc, ok := raw.(*net.UnixConn)
	if !ok {
		_ = raw.Close()
		return nil, fmt.Errorf("dial %s: not a Unix socket: %w", path, ErrConnect)
	}
	return newDisplay(uc), nil
}

// DialFD takes ownership of an already-connected file descriptor.
func DialFD(fd int) (*Display, error) {
	if fd < 0 {
		return nil, fmt.Errorf("dial fd %d: %w", fd, ErrSource)
	}
	f := os.NewFile(uintptr(fd), "wayland-socket")
	if f == nil {
		return nil, fmt.Errorf("dial fd %d: %w", fd, ErrConnect)
	}
	raw, err := net.FileConn(f)
	_ = f.Close()
	if err != nil {
		return nil, fmt.Errorf("dial fd %d: %w: %w", fd, ErrConnect, err)
	}
	uc, ok := raw.(*net.UnixConn)
	if !ok {
		_ = raw.Close()
		return nil, fmt.Errorf("dial fd %d: not a Unix socket: %w", fd, ErrConnect)
	}
	return newDisplay(uc), nil
}

func resolveSource() (string, int, error) {
	if s := os.Getenv("WAYLAND_SOCKET"); s != "" {
		fd, err := strconv.Atoi(s)
		if err != nil || fd < 0 {
			return "", -1, fmt.Errorf("WAYLAND_SOCKET=%q: %w", s, ErrSource)
		}
		return "", fd, nil
	}
	display := os.Getenv("WAYLAND_DISPLAY")
	if display == "" {
		display = "wayland-0"
	}
	if strings.HasPrefix(display, "/") {
		return display, -1, nil
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", -1, fmt.Errorf("WAYLAND_DISPLAY=%q without XDG_RUNTIME_DIR: %w", display, ErrSource)
	}
	return runtimeDir + "/" + display, -1, nil
}

func newDisplay(uc *net.UnixConn) *Display {
	c := &conn{
		uc:      uc,
		objects: make(map[uint32]*Proxy),
		nextID:  2,
	}
	p := &Proxy{
		conn:    c,
		id:      1,
		version: 1,
		iface:   mustInterface("wl_display"),
		state:   stateAlive,
	}
	c.objects[1] = p
	return NewDisplay(p)
}
