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
