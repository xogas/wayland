package wire

import (
	"encoding/binary"
	"testing"
)

// SendMessage rejects unaligned payloads, so every field Writer emits must be
// aligned; a type without padding breaks all traffic, not just its own
// message.
func TestWriterAlwaysEmitsAlignedPayload(t *testing.T) {
	for _, s := range []string{"", "a", "ab", "abc", "abcd", "abcde", "abcdefg"} {
		w := &Writer{}
		if err := w.String(s); err != nil {
			t.Fatal(err)
		}
		if len(w.Bytes())%4 != 0 {
			t.Fatalf("String(%q) produced %d bytes", s, len(w.Bytes()))
		}
	}
	for n := range 9 {
		w := &Writer{}
		if err := w.Array(make([]byte, n)); err != nil {
			t.Fatal(err)
		}
		if len(w.Bytes())%4 != 0 {
			t.Fatalf("Array(%d bytes) produced %d bytes", n, len(w.Bytes()))
		}
	}

	var w Writer
	_ = w.Int32(1)
	_ = w.Uint32(2)
	_ = w.Fixed(3)
	_ = w.Object(4)
	_ = w.NewID(5)
	_ = w.Fd(6)
	if len(w.Bytes())%4 != 0 {
		t.Fatalf("mixed fields produced %d bytes", len(w.Bytes()))
	}
}

func TestStringLengthCountsNUL(t *testing.T) {
	tests := []struct {
		in   string
		want uint32
	}{
		{"", 1},
		{"a", 2},
		{"abc", 4},
		{"abcd", 5},
	}
	for _, tt := range tests {
		w := &Writer{}
		if err := w.String(tt.in); err != nil {
			t.Fatal(err)
		}
		if got := binary.NativeEndian.Uint32(w.Bytes()); got != tt.want {
			t.Errorf("length prefix for %q = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestEmbeddedNULSurvivesTheCodec(t *testing.T) {
	// Not valid C-string data, but the byte count is preserved.
	w := &Writer{}
	if err := w.String("a\x00b"); err != nil {
		t.Fatal(err)
	}
	got, err := NewReader(w.Bytes(), nil).String()
	if err != nil {
		t.Fatalf("String: %v", err)
	}
	if got != "a\x00b" {
		t.Fatalf("String = %q, want %q", got, "a\x00b")
	}
}

func TestWriterResetDropsPayloadAndFDs(t *testing.T) {
	// SendRequest reuses one Writer, so a retained fd list would ride along on
	// the next request.
	w := &Writer{}
	_ = w.Uint32(1)
	_ = w.Fd(3)
	w.Reset()
	if got := len(w.Bytes()); got != 0 {
		t.Fatalf("Reset left %d payload bytes", got)
	}
	if got := len(w.Fds()); got != 0 {
		t.Fatalf("Reset left %d fds", got)
	}
}

type testRequest struct {
	a int32
	b string
}

func (r *testRequest) Marshal(w *Writer) error {
	if err := w.Int32(r.a); err != nil {
		return err
	}
	return w.String(r.b)
}

// Covers the generated-code path.
func TestMarshalerRoundTrip(t *testing.T) {
	in := &testRequest{a: -12345, b: "hello"}
	w := &Writer{}
	if err := in.Marshal(w); err != nil {
		t.Fatal(err)
	}
	if len(w.Bytes())%4 != 0 {
		t.Fatalf("marshal produced %d bytes", len(w.Bytes()))
	}

	r := NewReader(w.Bytes(), nil)
	a, err := r.Int32()
	if err != nil || a != in.a {
		t.Fatalf("Int32 = %d, %v", a, err)
	}
	b, err := r.String()
	if err != nil || b != in.b {
		t.Fatalf("String = %q, %v", b, err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("%d trailing bytes after a full decode", r.Remaining())
	}
}
