package wayland

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

// TestWireRoundtrip covers AC-W01: every argument type, null/empty/non-empty
// strings and arrays, fixed-point endpoints, fd, new_id and object values.
func TestWireRoundtrip(t *testing.T) {
	rfd, wfd := pipeFDs(t)
	defer func() { _ = syscall.Close(rfd) }()
	defer func() { _ = syscall.Close(wfd) }()
	const objectID = uint32(9001)

	run := func(name string, specs []ArgSpec, vals []Arg, prep func(*conn)) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			iface := testInterface("s03_wire", 1, []Message{{Name: "msg", Since: 1, Args: specs}}, nil)
			c := &conn{objects: make(map[uint32]*Proxy), nextID: 2}
			p := &Proxy{conn: c, id: 1, version: 1, iface: iface}
			c.objects[1] = p
			if prep != nil {
				prep(c)
			}
			data, fds, err := encodeRequest(p, 0, iface.Requests[0], vals)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if len(data)%4 != 0 {
				t.Fatalf("encoded length %d is not 4-byte aligned", len(data))
			}
			if got := int(binary.LittleEndian.Uint32(data[4:8]) >> 16); got != len(data) {
				t.Fatalf("header size = %d, len = %d", got, len(data))
			}
			// Dynamic new_id targets exist only so the encoder can resolve
			// the interface/version; the decoder creates the object itself.
			for i, as := range specs {
				if as.Type == ArgNewID && as.Interface == "" && vals[i].u32 != 0 {
					delete(c.objects, vals[i].u32)
				}
			}
			c.fds = append([]int(nil), fds...)
			got, err := c.decodeArgs(data[8:], specs, p)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			compareWireValues(t, specs, vals, got)
		})
	}

	run("int", []ArgSpec{{Name: "v", Type: ArgInt}}, []Arg{Int(-7)}, nil)
	run("uint", []ArgSpec{{Name: "v", Type: ArgUint}}, []Arg{Uint(42)}, nil)
	run("fixed_negative", []ArgSpec{{Name: "v", Type: ArgFixed}}, []Arg{Fixed(-1.5)}, nil)
	run("fixed_zero", []ArgSpec{{Name: "v", Type: ArgFixed}}, []Arg{Fixed(0)}, nil)
	run("fixed_positive", []ArgSpec{{Name: "v", Type: ArgFixed}}, []Arg{Fixed(1.25)}, nil)
	run("string_null", []ArgSpec{{Name: "v", Type: ArgString, AllowNull: true}}, []Arg{String(nil)}, nil)
	run("string_empty", []ArgSpec{{Name: "v", Type: ArgString}}, []Arg{String(strPtr(""))}, nil)
	run("string_value", []ArgSpec{{Name: "v", Type: ArgString}}, []Arg{String(strPtr("hello"))}, nil)
	run("array_null", []ArgSpec{{Name: "v", Type: ArgArray, AllowNull: true}}, []Arg{Array(nil)}, nil)
	run("array_empty", []ArgSpec{{Name: "v", Type: ArgArray}}, []Arg{Array([]byte{})}, nil)
	run("array_value", []ArgSpec{{Name: "v", Type: ArgArray}}, []Arg{Array([]byte{1, 2, 3, 4, 5})}, nil)
	run("object_null", []ArgSpec{{Name: "v", Type: ArgObject, AllowNull: true}}, []Arg{Object(0)}, nil)
	run("object_value", []ArgSpec{{Name: "v", Type: ArgObject}}, []Arg{Object(objectID)}, func(c *conn) {
		c.objects[objectID] = &Proxy{conn: c, id: objectID, iface: mustInterface("wl_registry"), version: 1}
	})
	run("new_id_null", []ArgSpec{{Name: "v", Type: ArgNewID, AllowNull: true}}, []Arg{NewID(0)}, nil)
	run("new_id_value", []ArgSpec{{Name: "v", Type: ArgNewID, Interface: "wl_registry"}}, []Arg{NewID(9002)}, nil)
	run("new_id_dynamic", []ArgSpec{{Name: "v", Type: ArgNewID}}, []Arg{NewID(9003)}, func(c *conn) {
		c.objects[9003] = &Proxy{conn: c, id: 9003, version: 1, iface: mustInterface("wl_registry")}
	})
	run("fd", []ArgSpec{{Name: "v", Type: ArgFD}}, []Arg{FD(rfd)}, nil)

	allSpecs := []ArgSpec{
		{Name: "i", Type: ArgInt},
		{Name: "u", Type: ArgUint},
		{Name: "f", Type: ArgFixed},
		{Name: "s", Type: ArgString, AllowNull: true},
		{Name: "a", Type: ArgArray, AllowNull: true},
		{Name: "fd", Type: ArgFD},
		{Name: "n", Type: ArgNewID, Interface: "wl_registry"},
		{Name: "o", Type: ArgObject, AllowNull: true},
	}
	allVals := []Arg{
		Int(-9), Uint(11), Fixed(-1.5), String(strPtr("abc")), Array([]byte{9, 8, 7}),
		FD(rfd), NewID(9004), Object(0),
	}
	run("all_types", allSpecs, allVals, nil)

	t.Run("little_endian", func(t *testing.T) {
		specs := []ArgSpec{{Name: "v", Type: ArgInt}}
		iface := testInterface("s03_endian", 1, []Message{{Name: "msg", Since: 1, Args: specs}}, nil)
		p := &Proxy{id: 1, version: 1, iface: iface}
		data, _, err := encodeRequest(p, 0, iface.Requests[0], []Arg{Int(0x01020304)})
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if got, want := data[8:12], []byte{0x04, 0x03, 0x02, 0x01}; !bytes.Equal(got, want) {
			t.Fatalf("little-endian int = % x, want % x", got, want)
		}
	})
}

func strPtr(s string) *string { return &s }

func compareWireValues(t *testing.T, specs []ArgSpec, want, got []Arg) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("decoded %d args, want %d", len(got), len(want))
	}
	for i, as := range specs {
		switch as.Type {
		case ArgInt:
			if want[i].Int() != got[i].Int() {
				t.Fatalf("arg %d int = %d, want %d", i, got[i].Int(), want[i].Int())
			}
		case ArgUint:
			if want[i].Uint() != got[i].Uint() {
				t.Fatalf("arg %d uint = %d, want %d", i, got[i].Uint(), want[i].Uint())
			}
		case ArgFixed:
			if want[i].Fixed() != got[i].Fixed() {
				t.Fatalf("arg %d fixed = %v, want %v", i, got[i].Fixed(), want[i].Fixed())
			}
		case ArgString:
			if (want[i].StringPtr() == nil) != (got[i].StringPtr() == nil) {
				t.Fatalf("arg %d string null mismatch: want %v, got %v", i, want[i].StringPtr(), got[i].StringPtr())
			}
			if want[i].StringPtr() != nil && want[i].String() != got[i].String() {
				t.Fatalf("arg %d string = %q, want %q", i, got[i].String(), want[i].String())
			}
		case ArgArray:
			if !bytes.Equal(want[i].Array(), got[i].Array()) {
				t.Fatalf("arg %d array = % x, want % x", i, got[i].Array(), want[i].Array())
			}
		case ArgFD:
			if want[i].FD() != got[i].FD() {
				t.Fatalf("arg %d fd = %d, want %d", i, got[i].FD(), want[i].FD())
			}
		case ArgNewID:
			if want[i].ID() != got[i].ID() {
				t.Fatalf("arg %d new_id = %d, want %d", i, got[i].ID(), want[i].ID())
			}
			if want[i].ID() != 0 {
				if got[i].Proxy() == nil {
					t.Fatalf("arg %d new_id proxy is nil", i)
				}
				if as.Interface != "" && got[i].Proxy().Interface().Name != as.Interface {
					t.Fatalf("arg %d new_id interface = %q, want %q", i, got[i].Proxy().Interface().Name, as.Interface)
				}
			}
		case ArgObject:
			if want[i].ID() != got[i].ID() {
				t.Fatalf("arg %d object = %d, want %d", i, got[i].ID(), want[i].ID())
			}
			if want[i].ID() != 0 && got[i].Proxy() == nil {
				t.Fatalf("arg %d object proxy is nil", i)
			}
		}
	}
}

// TestWireCoverage covers AC-W02: every generated request and event has at
// least one encode/decode case and the values round-trip.
func TestWireCoverage(t *testing.T) {
	var files []*os.File
	var fdPool []int
	for i := 0; i < 4; i++ {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("pipe: %v", err)
		}
		files = append(files, r, w)
		fdPool = append(fdPool, int(r.Fd()))
	}
	defer func() {
		for _, f := range files {
			_ = f.Close()
		}
	}()

	requests, events := 0, 0
	idc := uint32(10000)
	for _, iface := range allInterfaces {
		for i := range iface.Requests {
			requests++
			coverageMessage(t, iface, iface.Requests[i], fdPool, &idc)
		}
		for i := range iface.Events {
			events++
			coverageMessage(t, iface, iface.Events[i], fdPool, &idc)
		}
	}
	t.Logf("covered %d requests and %d events across %d interfaces", requests, events, len(allInterfaces))
	if requests == 0 || events == 0 {
		t.Fatalf("coverage set is empty: requests=%d events=%d", requests, events)
	}
}

func coverageMessage(t *testing.T, iface *Interface, spec Message, fdPool []int, idc *uint32) {
	t.Helper()
	variants := [][]Arg{nil}
	if hasAllowNull(spec) {
		variants = append(variants, nil)
	}
	for variant := range variants {
		args, objects, targets := buildCoverageArgs(spec, fdPool, idc, variant != 0)
		encConn := &conn{objects: make(map[uint32]*Proxy), nextID: 2}
		encParent := &Proxy{conn: encConn, id: 1, version: 1, iface: iface}
		encConn.objects[1] = encParent
		for id, p := range targets {
			p.conn = encConn
			encConn.objects[id] = p
		}
		data, fds, err := encodeRequest(encParent, 0, spec, args)
		if err != nil {
			t.Fatalf("%s %s: encode: %v", iface.Name, spec.Name, err)
		}
		decConn := &conn{objects: make(map[uint32]*Proxy), nextID: 2}
		decParent := &Proxy{conn: decConn, id: 1, version: 1, iface: iface}
		decConn.objects[1] = decParent
		for id, p := range objects {
			p.conn = decConn
			decConn.objects[id] = p
		}
		decConn.fds = append([]int(nil), fds...)
		got, err := decConn.decodeArgs(data[8:], spec.Args, decParent)
		if err != nil {
			t.Fatalf("%s %s: decode: %v", iface.Name, spec.Name, err)
		}
		compareWireValues(t, spec.Args, args, got)
	}
}

func hasAllowNull(spec Message) bool {
	for _, as := range spec.Args {
		if as.AllowNull {
			return true
		}
	}
	return false
}

func buildCoverageArgs(spec Message, fdPool []int, idc *uint32, null bool) ([]Arg, map[uint32]*Proxy, map[uint32]*Proxy) {
	args := make([]Arg, 0, len(spec.Args))
	objects := make(map[uint32]*Proxy)
	targets := make(map[uint32]*Proxy)
	for i, as := range spec.Args {
		switch as.Type {
		case ArgInt:
			args = append(args, Int(-3))
		case ArgUint:
			args = append(args, Uint(7))
		case ArgFixed:
			args = append(args, Fixed(1.25))
		case ArgString:
			if null && as.AllowNull {
				args = append(args, String(nil))
			} else if i%2 == 0 {
				args = append(args, String(strPtr("wire")))
			} else {
				args = append(args, String(strPtr("")))
			}
		case ArgArray:
			if null && as.AllowNull {
				args = append(args, Array(nil))
			} else {
				args = append(args, Array([]byte{1, 2, 3, 4}))
			}
		case ArgFD:
			args = append(args, FD(fdPool[i%len(fdPool)]))
		case ArgNewID:
			if null && as.AllowNull {
				args = append(args, NewID(0))
			} else {
				*idc = *idc + 1
				if as.Interface == "" {
					id := 0x03000000 + *idc
					targets[id] = &Proxy{id: id, version: 1, iface: mustInterface("wl_registry")}
					args = append(args, NewID(id))
				} else {
					args = append(args, NewID(0x01000000+*idc))
				}
			}
		case ArgObject:
			if null && as.AllowNull {
				args = append(args, Object(0))
			} else {
				*idc = *idc + 1
				id := 0x02000000 + *idc
				objects[id] = &Proxy{id: id, version: 1, iface: ifaceForNil(as.Interface)}
				args = append(args, Object(id))
			}
		}
	}
	return args, objects, targets
}

func ifaceForNil(name string) *Interface {
	if name == "" {
		return mustInterface("wl_registry")
	}
	iface, ok := InterfaceByName(name)
	if !ok {
		return mustInterface("wl_registry")
	}
	return iface
}

// TestWireMalformed covers AC-W03: truncated, undersized and misaligned wire
// data returns ErrMalformedMessage without panicking.
func TestWireMalformed(t *testing.T) {
	c := &conn{objects: make(map[uint32]*Proxy)}
	parent := &Proxy{version: 1}
	decode := func(name string, body []byte, specs []ArgSpec) {
		t.Helper()
		if _, err := c.decodeArgs(body, specs, parent); !errors.Is(err, ErrMalformedMessage) {
			t.Fatalf("%s: decode = %v, want ErrMalformedMessage", name, err)
		}
	}
	decode("truncated_int", nil, []ArgSpec{{Name: "i", Type: ArgInt}})
	decode("string_length_truncated", []byte{5, 0}, []ArgSpec{{Name: "s", Type: ArgString}})
	decode("string_body_truncated", appendU32(nil, 5), []ArgSpec{{Name: "s", Type: ArgString}})
	decode("string_missing_nul", append(appendU32(nil, 3), []byte("hiX")...), []ArgSpec{{Name: "s", Type: ArgString}})
	decode("array_body_truncated", appendU32(nil, 8), []ArgSpec{{Name: "a", Type: ArgArray}})
	decode("trailing_bytes", func() []byte {
		b := appendU32(nil, 2)
		b = append(b, 'h', 0)
		b = pad4(b)
		return append(b, 0, 0, 0, 1)
	}(), []ArgSpec{{Name: "s", Type: ArgString}})
	decode("unknown_arg_type", nil, []ArgSpec{{Name: "x", Type: ArgType(99)}})

	for _, tc := range []struct {
		name string
		size uint16
	}{
		{"size_zero", 0},
		{"size_four", 4},
		{"size_unaligned", 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := unixPair(t)
			d := newDisplay(a)
			defer d.Close()
			defer b.Close()
			writeRawHeader(t, b, 1, 0, tc.size)
			if err := d.Dispatch(); !errors.Is(err, ErrMalformedMessage) {
				t.Fatalf("dispatch = %v, want ErrMalformedMessage", err)
			}
			if !d.p.conn.isClosed() {
				t.Fatal("connection not closed after malformed frame")
			}
		})
	}
}

// TestWireFD covers AC-W04.
func TestWireFD(t *testing.T) {
	t.Run("two_and_one", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		defer d.Close()
		defer b.Close()
		iface := testInterface("s03_fd", 1, []Message{
			{Name: "two", Since: 1, Args: []ArgSpec{{Name: "a", Type: ArgFD}, {Name: "b", Type: ArgFD}}},
			{Name: "one", Since: 1, Args: []ArgSpec{{Name: "a", Type: ArgFD}}},
		}, nil)
		p := newTestProxy(t, d, iface, 1, nil)

		r1, w1 := pipeFDs(t)
		r2, w2 := pipeFDs(t)
		r3, w3 := pipeFDs(t)
		defer func() {
			for _, fd := range []int{r1, w1, r2, w2, r3, w3} {
				_ = syscall.Close(fd)
			}
		}()

		if err := p.Send(0, FD(r1), FD(r2)); err != nil {
			t.Fatalf("send two fds: %v", err)
		}
		srv := newTestServer(b)
		objectID, opcode, body, fds := srv.readFrame(t)
		if objectID != p.ID() || opcode != 0 || len(body) != 0 {
			t.Fatalf("frame = id %d op %d body %d", objectID, opcode, len(body))
		}
		if len(fds) != 2 {
			t.Fatalf("received %d fds, want 2", len(fds))
		}
		if !sameFD(t, fds[0], r1) || !sameFD(t, fds[1], r2) {
			t.Fatalf("fd order mismatch")
		}
		if err := p.Send(1, FD(r3)); err != nil {
			t.Fatalf("send one fd: %v", err)
		}
		_, _, _, fds = srv.readFrame(t)
		if len(fds) != 1 || !sameFD(t, fds[0], r3) {
			t.Fatalf("single fd = %v", fds)
		}
		// The library never closes descriptors supplied by the caller.
		_, _ = fdIdentity(t, r1)
		_, _ = fdIdentity(t, r2)
		_, _ = fdIdentity(t, r3)
		if err := d.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		// Received descriptors remain caller-owned after the connection closes.
		_, _ = fdIdentity(t, fds[0])
		_, _ = fdIdentity(t, r1)
	})

	t.Run("skipped_event_fd_closed", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		defer d.Close()
		defer b.Close()
		iface := testInterface("s03_fd_event", 1, nil,
			[]Message{{Name: "data", Since: 1, Args: []ArgSpec{{Name: "fd", Type: ArgFD}}}})
		p := newTestProxy(t, d, iface, 1, nil)

		rfd, wfd := pipeFDs(t)
		defer func() { _ = syscall.Close(rfd) }()
		if err := syscall.SetNonblock(rfd, true); err != nil {
			t.Fatalf("set nonblock: %v", err)
		}
		writeFrameMsg(t, b, p.ID(), 0, nil, []int{wfd})
		_ = syscall.Close(wfd)
		if err := d.Dispatch(); !errors.Is(err, ErrNoHandler) {
			t.Fatalf("dispatch = %v, want ErrNoHandler", err)
		}
		if d.p.conn.isClosed() {
			t.Fatal("connection closed on ErrNoHandler")
		}
		var buf [1]byte
		n, err := syscall.Read(rfd, buf[:])
		if n != 0 || err != nil {
			t.Fatalf("received fd was not closed by the library: n=%d err=%v", n, err)
		}
	})

	t.Run("destroy_pending_event_fd_closed", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		defer d.Close()
		defer b.Close()
		iface := testInterface("s03_fd_pending", 1,
			[]Message{{Name: "destroy", Since: 1, Destructor: true}},
			[]Message{{Name: "data", Since: 1, Args: []ArgSpec{{Name: "fd", Type: ArgFD}}}})
		p := newTestProxy(t, d, iface, 1, nil)
		if err := p.Send(0); err != nil {
			t.Fatalf("destroy: %v", err)
		}
		rfd, wfd := pipeFDs(t)
		defer func() { _ = syscall.Close(rfd) }()
		if err := syscall.SetNonblock(rfd, true); err != nil {
			t.Fatalf("set nonblock: %v", err)
		}
		writeFrameMsg(t, b, p.ID(), 0, nil, []int{wfd})
		_ = syscall.Close(wfd)
		if err := d.Dispatch(); err != nil {
			t.Fatalf("dispatch pending event: %v", err)
		}
		var buf [1]byte
		n, err := syscall.Read(rfd, buf[:])
		if n != 0 || err != nil {
			t.Fatalf("discarded event fd was not closed: n=%d err=%v", n, err)
		}
	})

	t.Run("msg_ctrunc", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		defer d.Close()
		defer b.Close()
		d.p.conn.oobLen = 16
		iface := testInterface("s03_fd_ctrunc", 1, nil,
			[]Message{{Name: "data", Since: 1, Args: []ArgSpec{{Name: "a", Type: ArgFD}, {Name: "b", Type: ArgFD}}}})
		p := newTestProxy(t, d, iface, 1, nil)
		r1, w1 := pipeFDs(t)
		r2, w2 := pipeFDs(t)
		defer func() {
			for _, fd := range []int{r1, w1, r2, w2} {
				_ = syscall.Close(fd)
			}
		}()
		writeFrameMsg(t, b, p.ID(), 0, nil, []int{w1, w2})
		if err := d.Dispatch(); !errors.Is(err, ErrMalformedMessage) {
			t.Fatalf("dispatch = %v, want ErrMalformedMessage", err)
		}
		if !d.p.conn.isClosed() {
			t.Fatal("connection not closed after MSG_CTRUNC")
		}
	})

	t.Run("partial_write_ancillary", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		defer d.Close()
		defer b.Close()
		iface := testInterface("s03_fd_partial", 1, []Message{
			{Name: "big", Since: 1, Args: []ArgSpec{{Name: "a", Type: ArgFD}, {Name: "b", Type: ArgFD}, {Name: "data", Type: ArgArray}}},
		}, nil)
		p := newTestProxy(t, d, iface, 1, nil)
		if err := a.SetWriteBuffer(1024); err != nil {
			t.Fatalf("set write buffer: %v", err)
		}
		r1, w1 := pipeFDs(t)
		r2, w2 := pipeFDs(t)
		defer func() {
			for _, fd := range []int{r1, w1, r2, w2} {
				_ = syscall.Close(fd)
			}
		}()
		payload := make([]byte, 60000)
		for i := range payload {
			payload[i] = byte(i)
		}

		srv := newTestServer(b)
		type frameResult struct {
			objectID uint32
			opcode   uint16
			body     []byte
			fds      []int
			err      error
		}
		frameCh := make(chan frameResult, 1)
		go func() {
			objectID, opcode, body, fds, err := srv.nextFrame()
			frameCh <- frameResult{objectID, opcode, body, fds, err}
		}()
		sendCh := make(chan error, 1)
		go func() {
			sendCh <- p.Send(0, FD(r1), FD(r2), Array(payload))
		}()
		select {
		case err := <-sendCh:
			if err != nil {
				t.Fatalf("send: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("send did not finish")
		}
		var res frameResult
		select {
		case res = <-frameCh:
		case <-time.After(5 * time.Second):
			t.Fatal("server did not receive the frame")
		}
		if res.err != nil {
			t.Fatalf("server read: %v", res.err)
		}
		if res.objectID != p.ID() || res.opcode != 0 {
			t.Fatalf("frame = id %d op %d", res.objectID, res.opcode)
		}
		if len(res.fds) != 2 {
			t.Fatalf("received %d fds, want 2 (ancillary was repeated?)", len(res.fds))
		}
		if !sameFD(t, res.fds[0], r1) || !sameFD(t, res.fds[1], r2) {
			t.Fatalf("fd order mismatch after partial write")
		}
		wantBody := appendU32(nil, uint32(len(payload)))
		wantBody = append(wantBody, payload...)
		wantBody = pad4(wantBody)
		if !bytes.Equal(res.body, wantBody) {
			t.Fatalf("body mismatch: got %d bytes, want %d", len(res.body), len(wantBody))
		}
		_, _ = fdIdentity(t, r1)
		_, _ = fdIdentity(t, r2)
	})
}

// TestWireInvalidNull covers AC-W05.
func TestWireInvalidNull(t *testing.T) {
	run := func(name string, spec ArgSpec, arg Arg, allowed bool) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			a, b := unixPair(t)
			d := newDisplay(a)
			defer d.Close()
			defer b.Close()
			iface := testInterface("s03_null", 1, []Message{{Name: "msg", Since: 1, Args: []ArgSpec{spec}}}, nil)
			p := newTestProxy(t, d, iface, 1, nil)
			err := p.Send(0, arg)
			if !allowed {
				if !errors.Is(err, ErrInvalidArgument) {
					t.Fatalf("send = %v, want ErrInvalidArgument", err)
				}
				assertNoBytes(t, b)
				return
			}
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			srv := newTestServer(b)
			objectID, opcode, body, _ := srv.readFrame(t)
			if objectID != p.ID() || opcode != 0 {
				t.Fatalf("frame = id %d op %d", objectID, opcode)
			}
			if len(body) != 4 || binary.LittleEndian.Uint32(body) != 0 {
				t.Fatalf("null body = % x", body)
			}
		})
	}

	run("object_zero", ArgSpec{Name: "v", Type: ArgObject}, Object(0), false)
	run("new_id_zero", ArgSpec{Name: "v", Type: ArgNewID, Interface: "wl_registry"}, NewID(0), false)
	run("string_null", ArgSpec{Name: "v", Type: ArgString}, String(nil), false)
	run("array_null", ArgSpec{Name: "v", Type: ArgArray}, Array(nil), false)
	run("object_zero_allowed", ArgSpec{Name: "v", Type: ArgObject, AllowNull: true}, Object(0), true)
	run("new_id_zero_allowed", ArgSpec{Name: "v", Type: ArgNewID, Interface: "wl_registry", AllowNull: true}, NewID(0), true)
	run("string_null_allowed", ArgSpec{Name: "v", Type: ArgString, AllowNull: true}, String(nil), true)
	run("array_null_allowed", ArgSpec{Name: "v", Type: ArgArray, AllowNull: true}, Array(nil), true)

	t.Run("decode_non_allow_null", func(t *testing.T) {
		c := &conn{objects: make(map[uint32]*Proxy)}
		parent := &Proxy{version: 1}
		cases := [][]ArgSpec{
			{{Name: "s", Type: ArgString}},
			{{Name: "o", Type: ArgObject}},
			{{Name: "n", Type: ArgNewID, Interface: "wl_registry"}},
		}
		for _, specs := range cases {
			body := appendU32(nil, 0)
			if _, err := c.decodeArgs(body, specs, parent); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("%s: decode = %v, want ErrInvalidArgument", specs[0].Name, err)
			}
		}
	})
}

// TestWireInvalidFD covers AC-W06.
func TestWireInvalidFD(t *testing.T) {
	setup := func(t *testing.T) (*Display, *net.UnixConn, *Proxy) {
		t.Helper()
		a, b := unixPair(t)
		d := newDisplay(a)
		t.Cleanup(func() {
			_ = d.Close()
			_ = b.Close()
		})
		iface := testInterface("s03_fd_invalid", 1, []Message{
			{Name: "msg", Since: 1, Args: []ArgSpec{{Name: "fd", Type: ArgFD}}},
		}, nil)
		return d, b, newTestProxy(t, d, iface, 1, nil)
	}

	t.Run("negative", func(t *testing.T) {
		_, b, p := setup(t)
		if err := p.Send(0, FD(-1)); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("send fd -1 = %v, want ErrInvalidArgument", err)
		}
		assertNoBytes(t, b)
	})
	t.Run("closed", func(t *testing.T) {
		_, b, p := setup(t)
		rfd, wfd := pipeFDs(t)
		_ = syscall.Close(rfd)
		_ = syscall.Close(wfd)
		// No descriptor is opened between Close and Send, so rfd stays invalid.
		if err := p.Send(0, FD(rfd)); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("send closed fd %d = %v, want ErrInvalidArgument", rfd, err)
		}
		assertNoBytes(t, b)
	})
}

// TestWireOversize covers AC-W07.
func TestWireOversize(t *testing.T) {
	t.Run("oversize", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		defer d.Close()
		defer b.Close()
		iface := testInterface("s03_oversize", 1, []Message{
			{Name: "msg", Since: 1, Args: []ArgSpec{{Name: "data", Type: ArgArray}}},
		}, nil)
		p := newTestProxy(t, d, iface, 1, nil)
		if err := p.Send(0, Array(make([]byte, 65524))); !errors.Is(err, ErrMalformedMessage) {
			t.Fatalf("send = %v, want ErrMalformedMessage", err)
		}
		assertNoBytes(t, b)
	})

	t.Run("max_size", func(t *testing.T) {
		a, b := unixPair(t)
		d := newDisplay(a)
		defer d.Close()
		defer b.Close()
		iface := testInterface("s03_oversize", 1, []Message{
			{Name: "msg", Since: 1, Args: []ArgSpec{{Name: "data", Type: ArgArray}}},
		}, nil)
		p := newTestProxy(t, d, iface, 1, nil)
		if err := p.Send(0, Array(make([]byte, 65520))); err != nil {
			t.Fatalf("max-size send: %v", err)
		}
		srv := newTestServer(b)
		_, _, body, _ := srv.readFrame(t)
		if len(body) != 65524 {
			t.Fatalf("body length = %d, want 65524", len(body))
		}
	})
}
