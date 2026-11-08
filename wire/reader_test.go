package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"testing"
)

// check is unexported, and uint32 cannot go negative on 64-bit, so the
// negative-need branch is only reachable by calling it directly.
func TestCheckRejectsNegative(t *testing.T) {
	r := NewReader(make([]byte, 8), nil)
	for _, n := range []int{-1, -4, math.MinInt32} {
		if err := r.check(n); err == nil {
			t.Fatalf("check(%d) accepted a negative need", n)
		}
	}
}

// These go negative or overflow pos+n on 32-bit.
func TestHugeLengthPrefixIsAnError(t *testing.T) {
	for _, n := range []uint32{0xFFFFFFFF, 0xFFFFFFFE, 0x80000001, 0x80000000, 0x7FFFFFFF, maxPayload + 1} {
		t.Run(fmt.Sprintf("%#x", n), func(t *testing.T) {
			buf := make([]byte, 4)
			binary.NativeEndian.PutUint32(buf, n)
			if _, err := NewReader(buf, nil).String(); err == nil {
				t.Errorf("String accepted length %#x", n)
			}
			if _, err := NewReader(buf, nil).StringNullable(); err == nil {
				t.Errorf("StringNullable accepted length %#x", n)
			}
			if _, err := NewReader(buf, nil).Array(); err == nil {
				t.Errorf("Array accepted length %#x", n)
			}
		})
	}
}

func TestStringNULLIsProtocolError(t *testing.T) {
	buf := make([]byte, 4)
	if _, err := NewReader(buf, nil).String(); err == nil {
		t.Fatal("String accepted NULL")
	}
	s, err := NewReader(buf, nil).StringNullable()
	if err != nil {
		t.Fatalf("StringNullable: %v", err)
	}
	if s != nil {
		t.Fatalf("StringNullable(NULL) = %q, want nil", *s)
	}

	w := &Writer{}
	if err := w.StringNullable(nil); err != nil {
		t.Fatal(err)
	}
	if got := len(w.Bytes()); got != 4 {
		t.Fatalf("StringNullable(nil) wrote %d bytes, want just the 4-byte length", got)
	}
	if got := binary.NativeEndian.Uint32(w.Bytes()); got != 0 {
		t.Fatalf("StringNullable(nil) length prefix = %d, want 0", got)
	}
}

func TestEmptyStringIsNotNull(t *testing.T) {
	// Length 1, not 0: zero decodes as NULL.
	w := &Writer{}
	if err := w.String(""); err != nil {
		t.Fatal(err)
	}
	if got := len(w.Bytes()); got != 8 {
		t.Fatalf("empty string encoded in %d bytes, want 8 (4-byte prefix, NUL padded to 4)", got)
	}
	s, err := NewReader(w.Bytes(), nil).StringNullable()
	if err != nil {
		t.Fatalf("StringNullable: %v", err)
	}
	if s == nil || *s != "" {
		t.Fatalf("empty string decoded as NULL")
	}
}

func TestStringCorruptionIsAnError(t *testing.T) {
	tests := []struct {
		name string
		buf  []byte
	}{
		{"truncated data", []byte{5, 0, 0, 0, 'A', 'B'}},
		{"missing NUL", []byte{3, 0, 0, 0, 'A', 'B', 'C'}},
		{"truncated padding", []byte{2, 0, 0, 0, 'a', 0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewReader(tt.buf, nil).String(); err == nil {
				t.Fatal("accepted a corrupt string")
			}
		})
	}
}

func TestArrayCorruptionIsAnError(t *testing.T) {
	tests := []struct {
		name string
		buf  []byte
	}{
		{"truncated data", []byte{10, 0, 0, 0, 1, 2, 3, 4}},
		{"truncated padding", []byte{5, 0, 0, 0, 1, 2, 3, 4, 5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewReader(tt.buf, nil).Array(); err == nil {
				t.Fatal("accepted a corrupt array")
			}
		})
	}
}

func TestArrayNilAndEmptyCollapse(t *testing.T) {
	// One wire value for both.
	for _, in := range [][]byte{nil, {}} {
		w := &Writer{}
		if err := w.Array(in); err != nil {
			t.Fatal(err)
		}
		got, err := NewReader(w.Bytes(), nil).Array()
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("Array(%#v) = %#v, want non-nil empty", in, got)
		}
	}
}

func TestFDQueue(t *testing.T) {
	r := NewReader(nil, []int{10, 20})
	fd, err := r.Fd()
	if err != nil || fd != 10 {
		t.Fatalf("first Fd = %d, %v", fd, err)
	}
	if got := r.UnconsumedFDs(); len(got) != 1 || got[0] != 20 {
		t.Fatalf("UnconsumedFDs = %v, want [20]", got)
	}
	if _, err := r.Fd(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Fd(); !errors.Is(err, ErrNoFD) {
		t.Fatalf("exhausted Fd = %v, want ErrNoFD", err)
	}
}

func TestSetFDsKeepsPayloadCursor(t *testing.T) {
	buf := make([]byte, 4)
	binary.NativeEndian.PutUint32(buf, 0xDEADBEEF)
	r := NewReader(buf, nil)
	if v, err := r.Uint32(); err != nil || v != 0xDEADBEEF {
		t.Fatalf("Uint32 = %#x, %v", v, err)
	}
	r.SetFDs([]int{7})
	if r.Remaining() != 0 {
		t.Fatalf("SetFDs rewound the payload cursor, %d bytes left", r.Remaining())
	}
	if fd, err := r.Fd(); err != nil || fd != 7 {
		t.Fatalf("Fd after SetFDs = %d, %v", fd, err)
	}
}

func TestCloneIsolatesHandlers(t *testing.T) {
	buf := make([]byte, 8)
	binary.NativeEndian.PutUint32(buf[0:4], 1)
	binary.NativeEndian.PutUint32(buf[4:8], 2)
	r := NewReader(buf, []int{10, 20})

	clone := r.Clone()
	if v, _ := clone.Uint32(); v != 1 {
		t.Fatalf("clone first word = %d", v)
	}
	if _, err := clone.Fd(); err != nil {
		t.Fatal(err)
	}
	if r.Remaining() != 8 {
		t.Fatalf("clone consumed from the original: %d bytes read", 8-r.Remaining())
	}
	if got := r.UnconsumedFDs(); len(got) != 2 {
		t.Fatalf("clone consumed the original's fds: %v", got)
	}
}
