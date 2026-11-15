package wayland

import (
	"net"
	"os"
	"syscall"
	"testing"
)

func unixPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	mk := func(fd int, name string) *net.UnixConn {
		f := os.NewFile(uintptr(fd), name)
		raw, err := net.FileConn(f)
		_ = f.Close()
		if err != nil {
			t.Fatalf("fileconn: %v", err)
		}
		uc, ok := raw.(*net.UnixConn)
		if !ok {
			_ = raw.Close()
			t.Fatalf("fileconn returned %T", raw)
		}
		return uc
	}
	return mk(fds[0], "wayland-a"), mk(fds[1], "wayland-b")
}

type registryRecorder struct {
	globals []Global
}

func (r *registryRecorder) Global(g Global) { r.globals = append(r.globals, g) }

func (r *registryRecorder) GlobalRemove(name uint32) {
	for i := range r.globals {
		if r.globals[i].Name == name {
			r.globals = append(r.globals[:i], r.globals[i+1:]...)
			return
		}
	}
}

type callbackRecorder struct {
	done uint32
}

func (c *callbackRecorder) Done(v uint32) { c.done = v }
