package wire

import "math"

// Fixed is a 24.8 signed fixed-point number.
type Fixed int32

// Float64 returns the floating-point representation of f.
func (f Fixed) Float64() float64 {
	return float64(f) / 256.0
}

// Int returns the integer part of f (truncated toward zero).
func (f Fixed) Int() int32 {
	return int32(f) / 256
}

// FixedFromFloat64 converts a float64 to a 24.8 fixed-point number.
func FixedFromFloat64(v float64) Fixed {
	return Fixed(int32(math.Round(v * 256.0)))
}

// FixedFromInt converts an int32 to a 24.8 fixed-point number.
func FixedFromInt(v int32) Fixed {
	return Fixed(v << 8)
}

// ObjectID is a 32-bit object ID allocated by the server.
type ObjectID uint32

// NewID is a 32-bit object ID allocated by the client.
type NewID uint32

// Marshaler is implemented by types that can marshal themselves to the wire format.
type Marshaler interface {
	Marshal(*Writer) error
}

// Unmarshaler is implemented by types that can unmarshal themselves from the wire format.
type Unmarshaler interface {
	Unmarshal(*Reader) error
}
