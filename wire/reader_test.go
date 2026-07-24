package wire

import (
	"testing"
)

func TestReaderBufferUnderflow(t *testing.T) {
	cases := []struct {
		name string
		fn   func(*Reader) error
	}{
		{"Int32", func(r *Reader) error { _, err := r.Int32(); return err }},
		{"Uint32", func(r *Reader) error { _, err := r.Uint32(); return err }},
		{"Fixed", func(r *Reader) error { _, err := r.Fixed(); return err }},
		{"Object", func(r *Reader) error { _, err := r.Object(); return err }},
		{"NewID", func(r *Reader) error { _, err := r.NewID(); return err }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewReader([]byte{1, 2, 3}, nil)
			if err := c.fn(r); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestReaderStringErrors(t *testing.T) {
	// Truncated data: length=5 but only "AB" provided
	r := NewReader([]byte{5, 0, 0, 0, 'A', 'B'}, nil)
	if _, err := r.String(); err == nil {
		t.Error("expected error for truncated string data")
	}

	// Missing NUL terminator: length=3, "AB" but no NUL
	r = NewReader([]byte{3, 0, 0, 0, 'A', 'B'}, nil)
	if _, err := r.String(); err == nil {
		t.Error("expected error for missing NUL terminator")
	}

	// Padding truncated: length=2, "a\x00" but no padding bytes
	r = NewReader([]byte{2, 0, 0, 0, 'a', 0}, nil)
	if _, err := r.String(); err == nil {
		t.Error("expected error for truncated string padding")
	}
}

func TestReaderArrayErrors(t *testing.T) {
	// Truncated data: length=10 but only 4 bytes provided
	r := NewReader([]byte{10, 0, 0, 0, 1, 2, 3, 4}, nil)
	if _, err := r.Array(); err == nil {
		t.Error("expected error for truncated array data")
	}

	// Padding truncated: length=5 with data but no padding
	r = NewReader([]byte{5, 0, 0, 0, 1, 2, 3, 4, 5}, nil)
	if _, err := r.Array(); err == nil {
		t.Error("expected error for truncated array padding")
	}
}

func TestReaderFd(t *testing.T) {
	// Empty queue
	r := NewReader(nil, nil)
	if _, err := r.Fd(); err == nil {
		t.Error("expected EOF for empty fd queue")
	}

	// Consume all then EOF
	r = NewReader(nil, []int{10})
	fd, err := r.Fd()
	if err != nil {
		t.Fatal(err)
	}
	if fd != 10 {
		t.Errorf("got %d, want 10", fd)
	}
	if _, err := r.Fd(); err == nil {
		t.Error("expected EOF after exhausting fd queue")
	}
}

func TestReaderUnconsumedFDs(t *testing.T) {
	r := NewReader(nil, []int{10, 20, 30})

	if _, err := r.Fd(); err != nil {
		t.Fatal(err)
	}
	unconsumed := r.UnconsumedFDs()
	if len(unconsumed) != 2 || unconsumed[0] != 20 || unconsumed[1] != 30 {
		t.Errorf("got %v, want [20 30]", unconsumed)
	}

	if _, err := r.Fd(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Fd(); err != nil {
		t.Fatal(err)
	}
	if got := r.UnconsumedFDs(); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestReaderSetFDs(t *testing.T) {
	r := NewReader(nil, nil)
	r.SetFDs([]int{99})
	fd, err := r.Fd()
	if err != nil {
		t.Fatal(err)
	}
	if fd != 99 {
		t.Errorf("got %d, want 99", fd)
	}
	if got := r.UnconsumedFDs(); len(got) != 0 {
		t.Errorf("expected no unconsumed fds, got %v", got)
	}

	// SetFDs resets fdIdx
	r.SetFDs([]int{1, 2})
	if _, err := r.Fd(); err != nil {
		t.Fatal(err)
	}
	if got := r.UnconsumedFDs(); len(got) != 1 {
		t.Errorf("expected 1 unconsumed fd, got %v", got)
	}
}

func TestReaderClone(t *testing.T) {
	buf := []byte{10, 0, 0, 0, 20, 0, 0, 0} // native-endian uint32(10), uint32(20)
	r := NewReader(buf, nil)

	v1, err := r.Uint32()
	if err != nil || v1 != 10 {
		t.Fatalf("first read: got %d, want 10", v1)
	}

	clone := r.Clone()

	v2, err := r.Uint32()
	if err != nil || v2 != 20 {
		t.Fatalf("original second read: got %d, want 20", v2)
	}

	// Clone starts from same position as when cloned
	v2c, err := clone.Uint32()
	if err != nil || v2c != 20 {
		t.Fatalf("clone read: got %d, want 20", v2c)
	}

	if r.pos != 8 || clone.pos != 8 {
		t.Errorf("positions: original=%d clone=%d, both want 8", r.pos, clone.pos)
	}
}
