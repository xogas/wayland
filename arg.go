package wayland

import "math"

// Arg carries one request or event argument.
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

// Int builds an int argument.
func Int(v int32) Arg { return Arg{typ: ArgInt, i32: v} }

// Uint builds a uint argument.
func Uint(v uint32) Arg { return Arg{typ: ArgUint, u32: v} }

// Fixed builds a 24.8 fixed-point argument from a float64 value.
func Fixed(v float64) Arg { return Arg{typ: ArgFixed, f64: v} }

// String builds a string argument; nil is the wire null string.
func String(v *string) Arg { return Arg{typ: ArgString, str: v} }

// Array builds an array argument; nil is an empty/null array.
func Array(v []byte) Arg { return Arg{typ: ArgArray, bytes: v} }

// FD builds a file descriptor argument.
func FD(v int) Arg { return Arg{typ: ArgFD, fd: v} }

// NewID builds a new_id argument.
func NewID(id uint32) Arg { return Arg{typ: ArgNewID, u32: id} }

// Object builds an object argument; 0 is null.
func Object(id uint32) Arg { return Arg{typ: ArgObject, u32: id} }

// Int returns the int32 value.
func (a Arg) Int() int32 { return a.i32 }

// Uint returns the uint32 value.
func (a Arg) Uint() uint32 { return a.u32 }

// Fixed returns the fixed-point value as float64.
func (a Arg) Fixed() float64 { return a.f64 }

// String returns the string value; null becomes "".
func (a Arg) String() string {
	if a.str == nil {
		return ""
	}
	return *a.str
}

// StringPtr returns the string pointer, preserving null.
func (a Arg) StringPtr() *string { return a.str }

// Array returns the array bytes.
func (a Arg) Array() []byte { return a.bytes }

// FD returns the file descriptor.
func (a Arg) FD() int { return a.fd }

// Proxy returns the object reference for object/new_id event arguments.
func (a Arg) Proxy() *Proxy { return a.proxy }

// ID returns the raw object/new_id value.
func (a Arg) ID() uint32 { return a.u32 }

func (a Arg) fixedRaw() int32 {
	return int32(math.Round(a.f64 * 256))
}
