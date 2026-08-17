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
	var b [4]byte
	binary.NativeEndian.PutUint32(b[:], uint32(v))
	_, err := w.buf.Write(b[:])
	return err
}

// Uint32 writes an unsigned 32-bit integer in host byte order.
func (w *Writer) Uint32(v uint32) error {
	var b [4]byte
	binary.NativeEndian.PutUint32(b[:], v)
	_, err := w.buf.Write(b[:])
	return err
}

// Fixed writes a 24.8 fixed-point number.
func (w *Writer) Fixed(v Fixed) error {
	return w.Int32(int32(v))
}

// String writes a length-prefixed string for a non-nullable argument.
// The 4-byte length prefix includes the NUL terminator, so an empty string
// is encoded as length 1 followed by the NUL byte (matching libwayland).
// The encoded bytes are padded to a 4-byte boundary.
func (w *Writer) String(s string) error {
	n := len(s) + 1 // includes NUL
	if err := w.Uint32(uint32(n)); err != nil {
		return err
	}
	if _, err := w.buf.WriteString(s); err != nil {
		return err
	}
	if err := w.buf.WriteByte(0); err != nil {
		return err
	}
	pad := (4 - n%4) % 4
	for range pad {
		if err := w.buf.WriteByte(0); err != nil {
			return err
		}
	}
	return nil
}

// StringNullable writes a string for an allow-null argument.
// A nil pointer is encoded as length 0 (NULL on the wire); a non-nil
// pointer is encoded like String.
func (w *Writer) StringNullable(s *string) error {
	if s == nil {
		return w.Uint32(0)
	}
	return w.String(*s)
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
	for range pad {
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

// Reset clears the accumulated payload and file descriptors so the Writer can
// be reused for another message. The underlying buffer capacity is retained,
// making Reset cheaper than allocating a fresh Writer per message.
func (w *Writer) Reset() {
	w.buf.Reset()
	w.fds = w.fds[:0]
}

// Fds returns the accumulated file descriptors.
func (w *Writer) Fds() []int {
	return w.fds
}
