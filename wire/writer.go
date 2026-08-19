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

// Int32 writes v in host byte order.
func (w *Writer) Int32(v int32) error {
	var b [4]byte
	binary.NativeEndian.PutUint32(b[:], uint32(v))
	_, err := w.buf.Write(b[:])
	return err
}

// Uint32 writes v in host byte order.
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

// String writes s with a length prefix including the NUL terminator (an
// empty string is length 1, matching libwayland), padded to a 4-byte boundary.
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

// StringNullable writes v; nil is encoded as length 0 (NULL on the wire).
func (w *Writer) StringNullable(s *string) error {
	if s == nil {
		return w.Uint32(0)
	}
	return w.String(*s)
}

// Object writes v.
func (w *Writer) Object(v ObjectID) error {
	return w.Uint32(uint32(v))
}

// NewID writes v.
func (w *Writer) NewID(v NewID) error {
	return w.Uint32(uint32(v))
}

// Array writes v with a byte-count length prefix, padded to a 4-byte boundary.
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

// Bytes returns the accumulated payload.
func (w *Writer) Bytes() []byte {
	return w.buf.Bytes()
}

// Reset clears the payload and fds, retaining the underlying buffer capacity
// for reuse.
func (w *Writer) Reset() {
	w.buf.Reset()
	w.fds = w.fds[:0]
}

// Fds returns the accumulated fds.
func (w *Writer) Fds() []int {
	return w.fds
}
