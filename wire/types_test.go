package wire

import (
	"math"
	"testing"
)

const (
	fixedMaxFloat = float64(math.MaxInt32) / 256.0
	fixedMinFloat = float64(math.MinInt32) / 256.0
)

func TestFixedRoundTrip(t *testing.T) {
	values := []float64{0, 1.5, -1.5, 0.25, -0.25, 3.14159, -2.71828, 1000.5, -1000.5, fixedMaxFloat, fixedMinFloat}
	for _, v := range values {
		f := FixedFromFloat64(v)
		back := f.Float64()
		if math.Abs(back-v) > 1.0/256.0+1e-8 {
			t.Errorf("fixed round-trip: %v -> %v -> %v (diff %v)", v, f, back, back-v)
		}
	}
}

func TestFixedFromFloat64(t *testing.T) {
	tests := []struct {
		in  float64
		out Fixed
	}{
		{0.0, 0},
		{1.0, 256},
		{-1.0, -256},
		{1.5, 384},
		{-1.5, -384},
		{0.5, 128},
		{-0.5, -128},
		{fixedMaxFloat, Fixed(2147483647)},
		{fixedMinFloat, Fixed(-2147483648)},
	}
	for _, tt := range tests {
		got := FixedFromFloat64(tt.in)
		if got != tt.out {
			t.Errorf("FixedFromFloat64(%v) = %v, want %v", tt.in, got, tt.out)
		}
	}
}

func TestFixedInt(t *testing.T) {
	tests := []struct {
		f   Fixed
		out int32
	}{
		{0, 0},
		{256, 1},
		{255, 0},
		{257, 1},
		{-256, -1},
		{-255, 0},
		{-257, -1},
	}
	for _, tt := range tests {
		got := tt.f.Int()
		if got != tt.out {
			t.Errorf("Fixed(%d).Int() = %v, want %v", tt.f, got, tt.out)
		}
	}
}

func TestStringRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   string
		// wantLen is the on-the-wire length prefix (includes NUL).
		wantLen uint32
	}{
		{"empty", "", 1},
		{"ascii", "hello", 6},
		{"unicode", "héllo", 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &Writer{}
			if err := w.String(tt.in); err != nil {
				t.Fatalf("String: %v", err)
			}
			r := NewReader(w.Bytes(), nil)
			length, err := r.Uint32()
			if err != nil {
				t.Fatalf("length prefix: %v", err)
			}
			if length != tt.wantLen {
				t.Fatalf("length prefix: got %d, want %d", length, tt.wantLen)
			}
			r = NewReader(w.Bytes(), nil)
			got, err := r.String()
			if err != nil {
				t.Fatalf("read String: %v", err)
			}
			if got != tt.in {
				t.Fatalf("round trip: got %q, want %q", got, tt.in)
			}
		})
	}
}

func TestStringNullableRoundTrip(t *testing.T) {
	// nil encodes as length 0 (NULL on the wire).
	w := &Writer{}
	if err := w.StringNullable(nil); err != nil {
		t.Fatalf("StringNullable(nil): %v", err)
	}
	if len(w.Bytes()) != 4 {
		t.Fatalf("nil encoding: got %d bytes, want 4", len(w.Bytes()))
	}
	r := NewReader(w.Bytes(), nil)
	s, err := r.StringNullable()
	if err != nil {
		t.Fatalf("read nil: %v", err)
	}
	if s != nil {
		t.Fatalf("got %q, want nil", *s)
	}

	// Non-nil pointer, including empty string, encodes like String.
	for _, in := range []string{"", "hello"} {
		w = &Writer{}
		if err := w.StringNullable(&in); err != nil {
			t.Fatalf("StringNullable(%q): %v", in, err)
		}
		r = NewReader(w.Bytes(), nil)
		s, err = r.StringNullable()
		if err != nil {
			t.Fatalf("read %q: %v", in, err)
		}
		if s == nil || *s != in {
			t.Fatalf("round trip: got %v, want %q", s, in)
		}
	}
}

func TestFixedFromInt(t *testing.T) {
	tests := []struct {
		in  int32
		out Fixed
	}{
		{0, 0},
		{1, 256},
		{-1, -256},
		{100, 25600},
	}
	for _, tt := range tests {
		got := FixedFromInt(tt.in)
		if got != tt.out {
			t.Errorf("FixedFromInt(%v) = %v, want %v", tt.in, got, tt.out)
		}
	}
}
