package wire

import (
	"bytes"
	"encoding/binary"
)

// Writer accumulates wire-format payload data and associated file descriptors.
type Writer struct {
	buf bytes.Buffer
	fds []int
}

// Int32 writes a signed 32-bit integer in host byte order.
func (w *Writer) Int32(v int32) error {
	return binary.Write(&w.buf, binary.NativeEndian, v)
}

// Uint32 writes an unsigned 32-bit integer in host byte order.
func (w *Writer) Uint32(v uint32) error {
	return binary.Write(&w.buf, binary.NativeEndian, v)
}

// Fixed writes a 24.8 fixed-point number.
func (w *Writer) Fixed(v Fixed) error {
	return w.Int32(int32(v))
}

// String writes a length-prefixed string.
// The 4-byte length prefix includes the NUL terminator.
// The encoded bytes are padded to a 4-byte boundary.
// An empty string is encoded as a length of 0 with no further bytes.
func (w *Writer) String(s string) error {
	if s == "" {
		return w.Uint32(0)
	}
	byteLen := len(s) + 1
	if err := w.Uint32(uint32(byteLen)); err != nil {
		return err
	}
	if _, err := w.buf.WriteString(s); err != nil {
		return err
	}
	if err := w.buf.WriteByte(0); err != nil {
		return err
	}
	pad := (4 - byteLen%4) % 4
	for i := 0; i < pad; i++ {
		if err := w.buf.WriteByte(0); err != nil {
			return err
		}
	}
	return nil
}

// Object writes an object ID.
func (w *Writer) Object(v ObjectID) error {
	return w.Uint32(uint32(v))
}

// NewID writes a new object ID.
func (w *Writer) NewID(v NewID) error {
	return w.Uint32(uint32(v))
}

// Array writes a length-prefixed byte array.
// The 4-byte length prefix is the byte count (excluding padding).
// The data is padded to a 4-byte boundary.
func (w *Writer) Array(v []byte) error {
	if err := w.Uint32(uint32(len(v))); err != nil {
		return err
	}
	if _, err := w.buf.Write(v); err != nil {
		return err
	}
	pad := (4 - len(v)%4) % 4
	for i := 0; i < pad; i++ {
		if err := w.buf.WriteByte(0); err != nil {
			return err
		}
	}
	return nil
}

// Fd adds a file descriptor to be transmitted via SCM_RIGHTS.
func (w *Writer) Fd(fd int) error {
	w.fds = append(w.fds, fd)
	return nil
}

// Bytes returns the accumulated payload bytes.
func (w *Writer) Bytes() []byte {
	return w.buf.Bytes()
}

// Fds returns the accumulated file descriptors.
func (w *Writer) Fds() []int {
	return w.fds
}
