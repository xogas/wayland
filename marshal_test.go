package wayland

import (
	"errors"
	"os"
	"testing"
)

func wireTestInterface() (*Proxy, Message) {
	spec := Message{
		Name:  "all",
		Since: 1,
		Args: []ArgSpec{
			{Name: "i", Type: ArgInt},
			{Name: "u", Type: ArgUint},
			{Name: "f", Type: ArgFixed},
			{Name: "s", Type: ArgString, AllowNull: true},
			{Name: "a", Type: ArgArray, AllowNull: true},
			{Name: "fd", Type: ArgFD},
			{Name: "sn", Type: ArgString, AllowNull: true},
			{Name: "an", Type: ArgArray, AllowNull: true},
		},
	}
	iface := &Interface{Name: "test", Version: 1, Requests: []Message{spec}}
	return &Proxy{id: 1, version: 1, iface: iface}, spec
}

func TestWireRoundTrip(t *testing.T) {
	p, spec := wireTestInterface()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	t.Cleanup(func() { _ = w.Close() })
	s := "hi"
	args := []Arg{
		Int(-3),
		Uint(7),
		Fixed(1.25),
		String(&s),
		Array([]byte{1, 2, 3}),
		FD(int(r.Fd())),
		String(nil),
		Array(nil),
	}
	data, fds, err := encodeRequest(p, 0, spec, args)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(fds) != 1 || fds[0] != int(r.Fd()) {
		t.Fatalf("fds = %v, want [%d]", fds, r.Fd())
	}
	c := &conn{fds: []int{int(r.Fd())}}
	got, err := c.decodeArgs(data[8:], spec.Args, &Proxy{version: 1})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got[0].Int() != -3 {
		t.Fatalf("int = %d", got[0].Int())
	}
	if got[1].Uint() != 7 {
		t.Fatalf("uint = %d", got[1].Uint())
	}
	if got[2].Fixed() != 1.25 {
		t.Fatalf("fixed = %v", got[2].Fixed())
	}
	if got[3].String() != "hi" {
		t.Fatalf("string = %q", got[3].String())
	}
	if string(got[4].Array()) != string([]byte{1, 2, 3}) {
		t.Fatalf("array = %v", got[4].Array())
	}
	if got[5].FD() != int(r.Fd()) {
		t.Fatalf("fd = %d, want %d", got[5].FD(), r.Fd())
	}
	if got[6].StringPtr() != nil {
		t.Fatalf("null string = %q", got[6].String())
	}
	if len(got[7].Array()) != 0 {
		t.Fatalf("null array = %v", got[7].Array())
	}
}

func TestStringNullAndEmpty(t *testing.T) {
	p, spec := wireTestInterface()
	spec.Args = []ArgSpec{{Name: "s", Type: ArgString, AllowNull: true}}
	p.iface.Requests[0] = spec
	empty := ""
	args := []Arg{String(nil)}
	data, _, err := encodeRequest(p, 0, spec, args)
	if err != nil {
		t.Fatalf("encode null: %v", err)
	}
	c := &conn{}
	got, err := c.decodeArgs(data[8:], spec.Args, &Proxy{version: 1})
	if err != nil {
		t.Fatalf("decode null: %v", err)
	}
	if got[0].StringPtr() != nil {
		t.Fatal("null string decoded as non-nil")
	}
	args = []Arg{String(&empty)}
	data, _, err = encodeRequest(p, 0, spec, args)
	if err != nil {
		t.Fatalf("encode empty: %v", err)
	}
	got, err = c.decodeArgs(data[8:], spec.Args, &Proxy{version: 1})
	if err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	if got[0].StringPtr() == nil || got[0].String() != "" {
		t.Fatalf("empty string = %v", got[0].StringPtr())
	}
}

func TestMalformedDecode(t *testing.T) {
	c := &conn{}
	specs := []ArgSpec{{Name: "s", Type: ArgString, AllowNull: false}}
	body := appendU32(nil, 5)
	body = append(body, []byte("abcd")...)
	if _, err := c.decodeArgs(body, specs, &Proxy{version: 1}); !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("missing NUL = %v, want ErrMalformedMessage", err)
	}
	body = appendU32(nil, 3)
	body = append(body, []byte("hi\x00")...)
	body = pad4(body)
	body = appendU32(body, 0)
	if _, err := c.decodeArgs(body, specs, &Proxy{version: 1}); !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("trailing bytes = %v, want ErrMalformedMessage", err)
	}
}

func TestEncodeArgCount(t *testing.T) {
	p, spec := wireTestInterface()
	if _, _, err := encodeRequest(p, 0, spec, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("arg count = %v, want ErrInvalidArgument", err)
	}
}
