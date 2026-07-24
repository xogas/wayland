package wire

import (
	"bytes"
	"testing"
)

func TestScalars(t *testing.T) {
	fixedVal := FixedFromFloat64(1.5)

	cases := []struct {
		name  string
		write func(*Writer) error
		read  func(*Reader) (any, error)
		want  any
	}{
		{
			"int32",
			func(w *Writer) error { return w.Int32(-12345) },
			func(r *Reader) (any, error) { return r.Int32() },
			int32(-12345),
		},
		{
			"uint32",
			func(w *Writer) error { return w.Uint32(0xDEADBEEF) },
			func(r *Reader) (any, error) { return r.Uint32() },
			uint32(0xDEADBEEF),
		},
		{
			"fixed",
			func(w *Writer) error { return w.Fixed(fixedVal) },
			func(r *Reader) (any, error) { return r.Fixed() },
			fixedVal,
		},
		{
			"object",
			func(w *Writer) error { return w.Object(7) },
			func(r *Reader) (any, error) { return r.Object() },
			ObjectID(7),
		},
		{
			"newid",
			func(w *Writer) error { return w.NewID(42) },
			func(r *Reader) (any, error) { return r.NewID() },
			NewID(42),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := &Writer{}
			if err := c.write(w); err != nil {
				t.Fatal(err)
			}
			got, err := c.read(NewReader(w.Bytes(), nil))
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestString(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"single byte", "a"},
		{"aligned", "abcd"},
		{"unaligned", "abc"},
		{"nul embedded", "hello\x00world"},
		{"unicode", "Hello, 世界"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := &Writer{}
			if err := w.String(c.in); err != nil {
				t.Fatal(err)
			}
			b := w.Bytes()
			if len(b)%4 != 0 {
				t.Errorf("payload len %d not 4-byte aligned", len(b))
			}
			got, err := NewReader(b, nil).String()
			if err != nil {
				t.Fatal(err)
			}
			if got != c.in {
				t.Errorf("got %q, want %q", got, c.in)
			}
		})
	}
}

func TestArray(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"single", []byte{1}},
		{"unaligned", []byte{1, 2, 3}},
		{"aligned", []byte{1, 2, 3, 4}},
		{"multi", []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := &Writer{}
			if err := w.Array(c.in); err != nil {
				t.Fatal(err)
			}
			b := w.Bytes()
			if len(b)%4 != 0 {
				t.Errorf("payload len %d not 4-byte aligned", len(b))
			}
			got, err := NewReader(b, nil).Array()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, c.in) {
				t.Errorf("got %v, want %v", got, c.in)
			}
		})
	}
}

// testMarshal implements Marshaler/Unmarshaler for integration testing.
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
	if err := decoded.Unmarshal(NewReader(w.Bytes(), nil)); err != nil {
		t.Fatal(err)
	}
	if decoded.a != orig.a || decoded.b != orig.b {
		t.Errorf("got {%d, %q}, want {%d, %q}", decoded.a, decoded.b, orig.a, orig.b)
	}
}
