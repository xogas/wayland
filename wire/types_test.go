package wire

import (
	"math"
	"testing"
)

// Covers overflow, which Go leaves unspecified.
func TestFixedFromFloat64Clamps(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want Fixed
	}{
		{"overflow", 1e9, Fixed(math.MaxInt32)},
		{"underflow", -1e9, Fixed(math.MinInt32)},
		{"+Inf", math.Inf(1), Fixed(math.MaxInt32)},
		{"-Inf", math.Inf(-1), Fixed(math.MinInt32)},
		{"NaN", math.NaN(), 0},
		{"exact max", float64(math.MaxInt32) / 256, Fixed(math.MaxInt32)},
		{"exact min", float64(math.MinInt32) / 256, Fixed(math.MinInt32)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FixedFromFloat64(tt.in); got != tt.want {
				t.Fatalf("FixedFromFloat64(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestFixedFromFloat64RoundsHalfAwayFromZero(t *testing.T) {
	// 1/512 is exactly half a wire unit.
	if got := FixedFromFloat64(1.0 / 512); got != 1 {
		t.Fatalf("+half unit = %d, want 1", got)
	}
	if got := FixedFromFloat64(-1.0 / 512); got != -1 {
		t.Fatalf("-half unit = %d, want -1", got)
	}
}

func TestFixedIntTruncatesTowardZero(t *testing.T) {
	tests := []struct {
		in   Fixed
		want int32
	}{
		{255, 0},
		{-255, 0},
		{256, 1},
		{-256, -1},
		{257, 1},
		{-257, -1},
		{-511, -1},
	}
	for _, tt := range tests {
		if got := tt.in.Int32(); got != tt.want {
			t.Errorf("Fixed(%d).Int32() = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestFixedFromIntWraps(t *testing.T) {
	if got := FixedFromInt(1 << 23); got != Fixed(math.MinInt32) {
		t.Fatalf("FixedFromInt(1<<23) = %d, want MinInt32", int32(got))
	}
}

func TestFixedFloat64Boundaries(t *testing.T) {
	// Two's complement, hence asymmetric.
	if got := Fixed(math.MinInt32).Float64(); got != -8388608 {
		t.Fatalf("MinInt32.Float64() = %v, want -8388608", got)
	}
	if got := Fixed(math.MaxInt32).Float64(); got != 8388607.99609375 {
		t.Fatalf("MaxInt32.Float64() = %v, want 8388607.99609375", got)
	}
}
