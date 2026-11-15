package wayland

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func errorTestDisplay(t *testing.T) (*Display, *net.UnixConn) {
	t.Helper()
	a, b := unixPair(t)
	d := newDisplay(a)
	t.Cleanup(func() {
		_ = d.Close()
		_ = b.Close()
	})
	return d, b
}

// TestProtocolError covers AC-E01.
func TestProtocolError(t *testing.T) {
	d, b := errorTestDisplay(t)
	iface := testInterface("s03_error", 1, nil,
		[]Message{{Name: "e", Since: 1, Args: []ArgSpec{{Name: "n", Type: ArgUint}}}})
	p := newTestProxy(t, d, iface, 1, nil)

	msg := "bad request"
	spec := mustInterface("wl_display").Events[0]
	data, _, err := encodeRequest(d.p, 0, spec, []Arg{Object(p.ID()), Uint(5), String(&msg)})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	writeFrame(t, b, 1, 0, data[8:])

	err = d.Dispatch()
	var pe *ProtocolError
	if !errors.As(err, &pe) {
		t.Fatalf("dispatch = %v, want *ProtocolError", err)
	}
	if pe.ObjectID != p.ID() || pe.Interface != iface.Name || pe.Code != 5 || pe.Message != msg {
		t.Fatalf("protocol error = %+v", pe)
	}
	if !errors.Is(err, ErrProtocolViolation) {
		t.Fatal("ProtocolError does not match ErrProtocolViolation")
	}
	if !strings.Contains(err.Error(), iface.Name) || !strings.Contains(err.Error(), msg) {
		t.Fatalf("protocol error message lacks fields: %v", err)
	}
	if !d.p.conn.isClosed() {
		t.Fatal("connection not closed after protocol error")
	}
	if err := d.Dispatch(); !errors.Is(err, ErrClosed) {
		t.Fatalf("subsequent dispatch = %v, want ErrClosed", err)
	}
}

func TestProtocolErrorZeroObject(t *testing.T) {
	d, b := errorTestDisplay(t)
	msg := "global failure"
	body := appendU32(nil, 0)
	body = appendU32(body, 9)
	body = appendU32(body, uint32(len(msg)+1))
	body = append(body, msg...)
	body = append(body, 0)
	body = pad4(body)
	writeFrame(t, b, 1, 0, body)

	err := d.Dispatch()
	var pe *ProtocolError
	if !errors.As(err, &pe) {
		t.Fatalf("dispatch = %v, want *ProtocolError", err)
	}
	if pe.ObjectID != 0 || pe.Interface != "" || pe.Code != 9 || pe.Message != msg {
		t.Fatalf("protocol error = %+v", pe)
	}
	if !errors.Is(err, ErrProtocolViolation) {
		t.Fatal("ProtocolError does not match ErrProtocolViolation")
	}
	if !d.p.conn.isClosed() {
		t.Fatal("connection not closed after protocol error")
	}
}

// TestIOError covers AC-E02.
func TestIOError(t *testing.T) {
	t.Run("dispatch_eof", func(t *testing.T) {
		d, b := errorTestDisplay(t)
		_ = b.Close()
		err := d.Dispatch()
		if !errors.Is(err, ErrIO) {
			t.Fatalf("dispatch = %v, want ErrIO", err)
		}
		if !d.p.conn.isClosed() {
			t.Fatal("connection not closed after EOF")
		}
	})

	t.Run("send_eof", func(t *testing.T) {
		d, b := errorTestDisplay(t)
		iface := testInterface("s03_io", 1, []Message{{Name: "ping", Since: 1}}, nil)
		p := newTestProxy(t, d, iface, 1, nil)
		_ = b.Close()
		var err error
		for i := 0; i < 100; i++ {
			if err = p.Send(0); err != nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if !errors.Is(err, ErrIO) {
			t.Fatalf("send after peer close = %v, want ErrIO", err)
		}
	})

	t.Run("mid_frame_eof", func(t *testing.T) {
		d, b := errorTestDisplay(t)
		writeRawHeader(t, b, 1, 0, 16)
		if _, err := b.Write([]byte{0, 0, 0, 0}); err != nil {
			t.Fatalf("write partial body: %v", err)
		}
		_ = b.Close()
		err := d.Dispatch()
		if !errors.Is(err, ErrIO) {
			t.Fatalf("dispatch = %v, want ErrIO", err)
		}
		if !d.p.conn.isClosed() {
			t.Fatal("connection not closed after mid-frame EOF")
		}
	})
}

// TestErrorCategories covers AC-E03: every runtime sentinel E-01 through E-16
// is distinguishable with errors.Is / errors.As, with operation context.
func TestErrorCategories(t *testing.T) {
	t.Run("E01_source", func(t *testing.T) {
		t.Setenv("WAYLAND_SOCKET", "")
		t.Setenv("WAYLAND_DISPLAY", "")
		t.Setenv("XDG_RUNTIME_DIR", "")
		_, err := Connect()
		if !errors.Is(err, ErrSource) || errors.Is(err, ErrConnect) {
			t.Fatalf("Connect = %v, want ErrSource", err)
		}
		if !strings.Contains(err.Error(), "WAYLAND") {
			t.Fatalf("error lacks source context: %v", err)
		}
	})

	t.Run("E02_connect", func(t *testing.T) {
		_, err := DialContext(context.Background(), filepath.Join(t.TempDir(), "missing.sock"))
		if !errors.Is(err, ErrConnect) || errors.Is(err, ErrSource) {
			t.Fatalf("DialContext = %v, want ErrConnect", err)
		}
		if !strings.Contains(err.Error(), "dial") {
			t.Fatalf("error lacks operation context: %v", err)
		}
	})

	t.Run("E03_closed", func(t *testing.T) {
		d, _ := errorTestDisplay(t)
		if err := d.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		err := d.Dispatch()
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("dispatch = %v, want ErrClosed", err)
		}
		if !strings.Contains(err.Error(), "dispatch") {
			t.Fatalf("error lacks operation context: %v", err)
		}
	})

	t.Run("E04_io", func(t *testing.T) {
		d, b := errorTestDisplay(t)
		_ = b.Close()
		err := d.Dispatch()
		if !errors.Is(err, ErrIO) {
			t.Fatalf("dispatch = %v, want ErrIO", err)
		}
		if !strings.Contains(err.Error(), "read") {
			t.Fatalf("error lacks operation context: %v", err)
		}
	})

	t.Run("E05_protocol_error", func(t *testing.T) {
		d, b := errorTestDisplay(t)
		p := newTestProxy(t, d, testInterface("s03_e05", 1, nil, nil), 1, nil)
		msg := "e05"
		spec := mustInterface("wl_display").Events[0]
		data, _, err := encodeRequest(d.p, 0, spec, []Arg{Object(p.ID()), Uint(2), String(&msg)})
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		writeFrame(t, b, 1, 0, data[8:])
		err = d.Dispatch()
		var pe *ProtocolError
		if !errors.As(err, &pe) {
			t.Fatalf("dispatch = %v, want *ProtocolError", err)
		}
		if pe.Interface != "s03_e05" || pe.Message != "e05" {
			t.Fatalf("protocol error = %+v", pe)
		}
	})

	t.Run("E06_malformed", func(t *testing.T) {
		d, b := errorTestDisplay(t)
		writeRawHeader(t, b, 1, 0, 4)
		err := d.Dispatch()
		if !errors.Is(err, ErrMalformedMessage) {
			t.Fatalf("dispatch = %v, want ErrMalformedMessage", err)
		}
		if !strings.Contains(err.Error(), "frame size") {
			t.Fatalf("error lacks operation context: %v", err)
		}
	})

	t.Run("E07_protocol_violation", func(t *testing.T) {
		d, b := errorTestDisplay(t)
		writeFrame(t, b, 999, 0, nil)
		err := d.Dispatch()
		if !errors.Is(err, ErrProtocolViolation) {
			t.Fatalf("dispatch = %v, want ErrProtocolViolation", err)
		}
		if !strings.Contains(err.Error(), "event object") {
			t.Fatalf("error lacks operation context: %v", err)
		}
	})

	t.Run("E08_version", func(t *testing.T) {
		d, _ := errorTestDisplay(t)
		reg, _ := newRegistryForTest(t, d)
		_, err := reg.Bind(Global{Name: 1, Interface: "xdg_surface", Version: 5}, 6)
		if !errors.Is(err, ErrVersion) {
			t.Fatalf("bind = %v, want ErrVersion", err)
		}
		if !strings.Contains(err.Error(), "xdg_surface") {
			t.Fatalf("error lacks interface context: %v", err)
		}
	})

	t.Run("E09_since", func(t *testing.T) {
		d, _ := errorTestDisplay(t)
		iface := testInterface("s03_e09", 4, []Message{
			{Name: "early", Since: 1},
			{Name: "late", Since: 4},
		}, nil)
		p := newTestProxy(t, d, iface, 3, nil)
		err := p.Send(1)
		if !errors.Is(err, ErrSince) {
			t.Fatalf("send = %v, want ErrSince", err)
		}
		if !strings.Contains(err.Error(), "s03_e09") || !strings.Contains(err.Error(), "late") {
			t.Fatalf("error lacks interface/message context: %v", err)
		}
	})

	t.Run("E10_invalid_argument", func(t *testing.T) {
		d, _ := errorTestDisplay(t)
		iface := testInterface("s03_e10", 1, []Message{
			{Name: "msg", Since: 1, Args: []ArgSpec{{Name: "s", Type: ArgString}}},
		}, nil)
		p := newTestProxy(t, d, iface, 1, nil)
		err := p.Send(0, String(nil))
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("send = %v, want ErrInvalidArgument", err)
		}
		if !strings.Contains(err.Error(), "s03_e10") || !strings.Contains(err.Error(), "msg") {
			t.Fatalf("error lacks interface/message context: %v", err)
		}
	})

	t.Run("E11_object_destroyed", func(t *testing.T) {
		d, _ := errorTestDisplay(t)
		iface := testInterface("s03_e11", 1, []Message{
			{Name: "destroy", Since: 1, Destructor: true},
		}, nil)
		p := newTestProxy(t, d, iface, 1, nil)
		if err := p.Send(0); err != nil {
			t.Fatalf("destroy: %v", err)
		}
		err := p.Send(0)
		if !errors.Is(err, ErrObjectDestroyed) {
			t.Fatalf("send = %v, want ErrObjectDestroyed", err)
		}
		if !strings.Contains(err.Error(), "send object") {
			t.Fatalf("error lacks operation context: %v", err)
		}
	})

	t.Run("E12_context_canceled", func(t *testing.T) {
		d, _ := errorTestDisplay(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := d.Roundtrip(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("roundtrip = %v, want context.Canceled", err)
		}
		if d.p.conn.isClosed() {
			t.Fatal("connection closed by canceled Roundtrip")
		}
	})

	t.Run("E15_dynamic_unknown", func(t *testing.T) {
		d, _ := errorTestDisplay(t)
		reg, _ := newRegistryForTest(t, d)
		_, err := reg.Bind(Global{Name: 1, Interface: "s03_e15_missing", Version: 1}, 1)
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("bind = %v, want ErrInvalidArgument", err)
		}
		if !strings.Contains(err.Error(), "bind") {
			t.Fatalf("error lacks operation context: %v", err)
		}
	})

	t.Run("E16_id_exhausted", func(t *testing.T) {
		d, _ := errorTestDisplay(t)
		d.p.conn.nextID = clientIDMax
		if _, err := d.p.conn.newObject(mustInterface("wl_registry"), 1, false); err != nil {
			t.Fatalf("new at max: %v", err)
		}
		_, err := d.p.conn.newObject(mustInterface("wl_registry"), 1, false)
		if !errors.Is(err, ErrIDExhausted) {
			t.Fatalf("new = %v, want ErrIDExhausted", err)
		}
		if !strings.Contains(err.Error(), "new wl_registry") {
			t.Fatalf("error lacks operation/interface context: %v", err)
		}
	})

	t.Run("ErrNoHandler", func(t *testing.T) {
		d, b := errorTestDisplay(t)
		iface := testInterface("s03_nohandler", 1, nil,
			[]Message{{Name: "data", Since: 1, Args: []ArgSpec{{Name: "n", Type: ArgUint}}}})
		p := newTestProxy(t, d, iface, 1, nil)
		writeFrame(t, b, p.ID(), 0, appendU32(nil, 1))
		err := d.Dispatch()
		if !errors.Is(err, ErrNoHandler) {
			t.Fatalf("dispatch = %v, want ErrNoHandler", err)
		}
		if !strings.Contains(err.Error(), "s03_nohandler") {
			t.Fatalf("error lacks interface context: %v", err)
		}
		if d.p.conn.isClosed() {
			t.Fatal("ErrNoHandler closed the connection")
		}
	})

	t.Run("ErrInvalidOperation", func(t *testing.T) {
		d, _ := errorTestDisplay(t)
		iface := testInterface("s03_invalid_op", 1, []Message{
			{Name: "ping", Since: 1},
		}, nil)
		p := newTestProxy(t, d, iface, 1, nil)
		if err := p.Send(0); err != nil {
			t.Fatalf("send: %v", err)
		}
		err := p.Release()
		if !errors.Is(err, ErrInvalidOperation) {
			t.Fatalf("release = %v, want ErrInvalidOperation", err)
		}
	})
}
