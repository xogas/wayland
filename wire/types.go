package wire

import "math"

// Fixed is wl_fixed: signed 24.8 fixed point.
type Fixed int32

func (f Fixed) Float64() float64 {
	return float64(f) / 256.0
}

// Int32 truncates toward zero, so -1/256 is 0 and not -1.
func (f Fixed) Int32() int32 {
	return int32(f) / 256
}

// FixedFromFloat64 rounds half away from zero and clamps, because Go's
// float-to-int overflow is unspecified. NaN is 0.
func FixedFromFloat64(v float64) Fixed {
	s := math.Round(v * 256.0)
	switch {
	case math.IsNaN(s):
		return 0
	case s >= math.MaxInt32:
		return Fixed(math.MaxInt32)
	case s <= math.MinInt32:
		return Fixed(math.MinInt32)
	}
	return Fixed(int32(s))
}

// FixedFromInt wraps on overflow, unlike FixedFromFloat64.
func FixedFromInt(v int32) Fixed {
	return Fixed(v << 8)
}

// ObjectID is server-assigned, NewID is client-allocated. Distinct types turn
// a swapped argument list into a compile error.
type ObjectID uint32

// NewID is client-allocated. See ObjectID.
type NewID uint32

// Marshaler is implemented by generated requests.
type Marshaler interface {
	Marshal(*Writer) error
}

// Unmarshaler is implemented by generated events.
type Unmarshaler interface {
	Unmarshal(*Reader) error
}
