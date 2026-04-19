package wire

import (
	"bytes"
	"testing"
)

func writerReader() (*Writer, *Reader) {
	w := &Writer{}
	_ = w.Int32(42)
	_ = w.Uint32(99)
	r := NewReader(w.Bytes(), w.Fds())
	return w, r
}

func TestInt32RoundTrip(t *testing.T) {
	w := &Writer{}
	if err := w.Int32(-12345); err != nil {
		t.Fatal(err)
	}
	r := NewReader(w.Bytes(), nil)
	got, err := r.Int32()
	if err != nil {
		t.Fatal(err)
	}
	if got != -12345 {
		t.Errorf("Int32 round-trip: got %d, want -12345", got)
	}
}

func TestUint32RoundTrip(t *testing.T) {
	w := &Writer{}
	if err := w.Uint32(0xDEADBEEF); err != nil {
		t.Fatal(err)
	}
	r := NewReader(w.Bytes(), nil)
	got, err := r.Uint32()
	if err != nil {
		t.Fatal(err)
	}
	if got != 0xDEADBEEF {
		t.Errorf("Uint32 round-trip: got %#x, want %#x", got, 0xDEADBEEF)
	}
}

func TestFixedRoundTripWire(t *testing.T) {
	w := &Writer{}
	f := FixedFromFloat64(1.5)
	if err := w.Fixed(f); err != nil {
		t.Fatal(err)
	}
	r := NewReader(w.Bytes(), nil)
	got, err := r.Fixed()
	if err != nil {
		t.Fatal(err)
	}
	if got != f {
		t.Errorf("Fixed round-trip: got %d, want %d", got, f)
	}
	if got.Float64() != 1.5 {
		t.Errorf("Fixed float: got %v, want 1.5", got.Float64())
	}
}

func TestObjectNewIDRoundTrip(t *testing.T) {
	w := &Writer{}
	if err := w.Object(7); err != nil {
		t.Fatal(err)
	}
	if err := w.NewID(42); err != nil {
		t.Fatal(err)
	}
	r := NewReader(w.Bytes(), nil)
	obj, err := r.Object()
	if err != nil {
		t.Fatal(err)
	}
	if obj != 7 {
		t.Errorf("Object: got %d, want 7", obj)
	}
	nid, err := r.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if nid != 42 {
		t.Errorf("NewID: got %d, want 42", nid)
	}
}

func TestWriterNewReader(t *testing.T) {
	w, r := writerReader()
	if w == nil || r == nil {
		t.Fatal("nil writer/reader")
	}
	v, err := r.Int32()
	if err != nil {
		t.Fatal(err)
	}
	if v != 42 {
		t.Errorf("first: got %d, want 42", v)
	}
	u, err := r.Uint32()
	if err != nil {
		t.Fatal(err)
	}
	if u != 99 {
		t.Errorf("second: got %d, want 99", u)
	}
}

func TestWriterBytes(t *testing.T) {
	w := &Writer{}
	_ = w.Int32(1)
	b := w.Bytes()
	if len(b) != 4 {
		t.Errorf("Bytes: len=%d, want 4", len(b))
	}
}

func TestWriterFds(t *testing.T) {
	w := &Writer{}
	_ = w.Fd(10)
	_ = w.Fd(20)
	fds := w.Fds()
	if len(fds) != 2 || fds[0] != 10 || fds[1] != 20 {
		t.Errorf("Fds: got %v, want [10 20]", fds)
	}
	if len(w.Bytes()) != 0 {
		t.Errorf("Fd should not affect Bytes length")
	}
}

// ========== String Tests ==========

func TestStringEmpty(t *testing.T) {
	w := &Writer{}
	if err := w.String(""); err != nil {
		t.Fatal(err)
	}
	b := w.Bytes()
	if len(b) != 4 {
		t.Errorf("empty string payload: len=%d, want 4", len(b))
	}
	// length field should be 0
	r := NewReader(b, nil)
	s, err := r.String()
	if err != nil {
		t.Fatal(err)
	}
	if s != "" {
		t.Errorf("empty string: got %q, want empty", s)
	}
	// No data left after reading empty string
	if _, err := r.Int32(); err == nil {
		t.Errorf("expected EOF after empty string")
	}
}

func TestStringAlign(t *testing.T) {
	for _, s := range []string{"a", "ab", "abc", "abcd", "abcde", "abcdef", "abcdefg", "abcdefgh"} {
		w := &Writer{}
		if err := w.String(s); err != nil {
			t.Fatal(err)
		}
		b := w.Bytes()
		if len(b)%4 != 0 {
			t.Errorf("String %q: payload len %d not 4-byte aligned", s, len(b))
		}
		r := NewReader(b, nil)
		got, err := r.String()
		if err != nil {
			t.Fatalf("String %q: read error: %v", s, err)
		}
		if got != s {
			t.Errorf("String %q: got %q", s, got)
		}
	}
}

func TestStringNUL(t *testing.T) {
	w := &Writer{}
	// String with embedded NUL (not typical in protocol, but wire format supports it)
	if err := w.String("hello\x00world"); err != nil {
		t.Fatal(err)
	}
	r := NewReader(w.Bytes(), nil)
	got, err := r.String()
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello\x00world" {
		t.Errorf("NUL string: got %q, want %q", got, "hello\x00world")
	}
}

func TestStringUnicode(t *testing.T) {
	s := "Hello, 世界"
	w := &Writer{}
	if err := w.String(s); err != nil {
		t.Fatal(err)
	}
	r := NewReader(w.Bytes(), nil)
	got, err := r.String()
	if err != nil {
		t.Fatal(err)
	}
	if got != s {
		t.Errorf("unicode: got %q, want %q", got, s)
	}
}

// ========== Array Tests ==========

func TestArrayEmpty(t *testing.T) {
	w := &Writer{}
	if err := w.Array(nil); err != nil {
		t.Fatal(err)
	}
	b := w.Bytes()
	if len(b) != 4 {
		t.Errorf("empty array payload: len=%d, want 4", len(b))
	}
	r := NewReader(b, nil)
	got, err := r.Array()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("empty array: got len=%d, want 0", len(got))
	}
}

func TestArrayAlign(t *testing.T) {
	for _, data := range [][]byte{
		{1},
		{1, 2},
		{1, 2, 3},
		{1, 2, 3, 4},
		{1, 2, 3, 4, 5},
	} {
		w := &Writer{}
		if err := w.Array(data); err != nil {
			t.Fatal(err)
		}
		b := w.Bytes()
		if len(b)%4 != 0 {
			t.Errorf("Array len=%d: payload len %d not 4-byte aligned", len(data), len(b))
		}
		r := NewReader(b, nil)
		got, err := r.Array()
		if err != nil {
			t.Fatalf("Array len=%d: read error: %v", len(data), err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("Array len=%d: got %v, want %v", len(data), got, data)
		}
	}
}

func TestArrayRoundTrip(t *testing.T) {
	data := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}
	w := &Writer{}
	if err := w.Array(data); err != nil {
		t.Fatal(err)
	}
	r := NewReader(w.Bytes(), nil)
	got, err := r.Array()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("Array: got %v, want %v", got, data)
	}
}

// ========== Reader Error Tests ==========

func TestReaderInt32EOF(t *testing.T) {
	r := NewReader([]byte{1, 2, 3}, nil)
	_, err := r.Int32()
	if err == nil {
		t.Errorf("expected error for short int32 read")
	}
}

func TestReaderUint32EOF(t *testing.T) {
	r := NewReader(nil, nil)
	_, err := r.Uint32()
	if err == nil {
		t.Errorf("expected error for empty buffer")
	}
}

func TestReaderStringTruncated(t *testing.T) {
	// Write string length 5 (4 chars + NUL) but only 2 bytes of data
	buf := []byte{5, 0, 0, 0, 'A', 'B'}
	r := NewReader(buf, nil)
	_, err := r.String()
	if err == nil {
		t.Errorf("expected error for truncated string")
	}
}

func TestReaderArrayTruncated(t *testing.T) {
	// Array length 10 but only 4 bytes of data
	buf := []byte{10, 0, 0, 0, 1, 2, 3, 4}
	r := NewReader(buf, nil)
	_, err := r.Array()
	if err == nil {
		t.Errorf("expected error for truncated array")
	}
}

func TestReaderFdEOF(t *testing.T) {
	r := NewReader(nil, []int{10})
	fd, err := r.Fd()
	if err != nil {
		t.Fatal(err)
	}
	if fd != 10 {
		t.Errorf("Fd: got %d, want 10", fd)
	}
	_, err = r.Fd()
	if err == nil {
		t.Errorf("expected EOF for exhausted fd queue")
	}
}

func TestReaderFdEmpty(t *testing.T) {
	r := NewReader(nil, nil)
	_, err := r.Fd()
	if err == nil {
		t.Errorf("expected EOF for empty fd queue")
	}
}

// ========== Marshaler / Unmarshaler ==========

type testMarshal struct {
	a int32
	b string
}

func (m *testMarshal) Marshal(w *Writer) error {
	if err := w.Int32(m.a); err != nil {
		return err
	}
	return w.String(m.b)
}

func (m *testMarshal) Unmarshal(r *Reader) error {
	var err error
	m.a, err = r.Int32()
	if err != nil {
		return err
	}
	m.b, err = r.String()
	return err
}

func TestMarshalerUnmarshaler(t *testing.T) {
	orig := &testMarshal{a: 12345, b: "hello"}
	w := &Writer{}
	if err := orig.Marshal(w); err != nil {
		t.Fatal(err)
	}
	decoded := &testMarshal{}
	r := NewReader(w.Bytes(), nil)
	if err := decoded.Unmarshal(r); err != nil {
		t.Fatal(err)
	}
	if decoded.a != orig.a || decoded.b != orig.b {
		t.Errorf("marshal/unmarshal: got {%d, %q}, want {%d, %q}", decoded.a, decoded.b, orig.a, orig.b)
	}
}

// ========== Composite Message ==========

func TestCompositeRoundTrip(t *testing.T) {
	w := &Writer{}
	_ = w.Object(1)
	_ = w.Uint32(0)
	_ = w.NewID(42)
	_ = w.String("get_registry")
	_ = w.Uint32(2)
	_ = w.NewID(43)
	r := NewReader(w.Bytes(), nil)

	obj, _ := r.Object()
	if obj != 1 {
		t.Errorf("obj: got %d", obj)
	}
	v, _ := r.Uint32()
	if v != 0 {
		t.Errorf("uint32: got %d", v)
	}
	nid, _ := r.NewID()
	if nid != 42 {
		t.Errorf("newid: got %d", nid)
	}
	s, _ := r.String()
	if s != "get_registry" {
		t.Errorf("string: got %q", s)
	}
	v2, _ := r.Uint32()
	if v2 != 2 {
		t.Errorf("uint32 2: got %d", v2)
	}
	nid2, _ := r.NewID()
	if nid2 != 43 {
		t.Errorf("newid 2: got %d", nid2)
	}
}
