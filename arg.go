package wayland

import "math"

// Arg is one wire argument.
type Arg struct {
	typ   ArgType
	i32   int32
	u32   uint32
	f64   float64
	str   *string
	bytes []byte
	fd    int
	proxy *Proxy
}

// Int returns an int argument.
func Int(v int32) Arg { return Arg{typ: ArgInt, i32: v} }

// Uint returns a uint argument.
func Uint(v uint32) Arg { return Arg{typ: ArgUint, u32: v} }

// Fixed returns a fixed-point argument.
func Fixed(v float64) Arg { return Arg{typ: ArgFixed, f64: v} }

// String returns a string argument; nil is null.
func String(v *string) Arg { return Arg{typ: ArgString, str: v} }

// Array returns an array argument; nil is null.
func Array(v []byte) Arg { return Arg{typ: ArgArray, bytes: v} }

// FD returns a file-descriptor argument.
func FD(v int) Arg { return Arg{typ: ArgFD, fd: v} }

// NewID returns a new_id argument.
func NewID(id uint32) Arg { return Arg{typ: ArgNewID, u32: id} }

// Object returns an object argument; 0 is null.
func Object(id uint32) Arg { return Arg{typ: ArgObject, u32: id} }

// Int returns the int32 value.
func (a Arg) Int() int32 { return a.i32 }

// Uint returns the uint32 value.
func (a Arg) Uint() uint32 { return a.u32 }

// Fixed returns the fixed-point value.
func (a Arg) Fixed() float64 { return a.f64 }

// String returns the string; null is empty.
func (a Arg) String() string {
	if a.str == nil {
		return ""
	}
	return *a.str
}

// StringPtr returns the string, preserving null.
func (a Arg) StringPtr() *string { return a.str }

// Array returns the array bytes.
func (a Arg) Array() []byte { return a.bytes }

// FD returns the file descriptor.
func (a Arg) FD() int { return a.fd }

// Proxy returns the referenced object.
func (a Arg) Proxy() *Proxy { return a.proxy }

// ID returns the raw object id.
func (a Arg) ID() uint32 { return a.u32 }

func (a Arg) fixedRaw() int32 {
	return int32(math.Round(a.f64 * 256))
}
