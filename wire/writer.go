package wire

import (
	"bytes"
	"encoding/binary"
)

// Writer builds one payload and its fd list. The error results are forced by
// Marshal's signature; nothing here can fail.
type Writer struct {
	buf bytes.Buffer
	fds []int
}

// One method per wire type, so an ObjectID cannot be passed where a NewID
// belongs.
func (w *Writer) Int32(v int32) error     { return w.put32(uint32(v)) }
func (w *Writer) Uint32(v uint32) error   { return w.put32(v) }
func (w *Writer) Fixed(v Fixed) error     { return w.put32(uint32(v)) }
func (w *Writer) Object(v ObjectID) error { return w.put32(uint32(v)) }
func (w *Writer) NewID(v NewID) error     { return w.put32(uint32(v)) }

func (w *Writer) put32(v uint32) error {
	var b [4]byte
	binary.NativeEndian.PutUint32(b[:], v)
	_, err := w.buf.Write(b[:])
	return err
}

// The length prefix counts the trailing NUL, so "" is length 1 and never 0.
// Embedded NULs pass through: this is not a C string.
func (w *Writer) String(s string) error {
	n := len(s) + 1
	if err := w.Uint32(uint32(n)); err != nil {
		return err
	}
	if _, err := w.buf.WriteString(s); err != nil {
		return err
	}
	if err := w.buf.WriteByte(0); err != nil {
		return err
	}
	return w.pad(n)
}

// nil is length 0; a non-nil pointer always goes through String, so "" stays
// length 1.
func (w *Writer) StringNullable(s *string) error {
	if s == nil {
		return w.Uint32(0)
	}
	return w.String(*s)
}

func (w *Writer) Array(v []byte) error {
	if err := w.Uint32(uint32(len(v))); err != nil {
		return err
	}
	if _, err := w.buf.Write(v); err != nil {
		return err
	}
	return w.pad(len(v))
}

func (w *Writer) pad(n int) error {
	for range (4 - n%4) % 4 {
		if err := w.buf.WriteByte(0); err != nil {
			return err
		}
	}
	return nil
}

// Fd queues a descriptor for the next SendMessage. The caller keeps ownership.
func (w *Writer) Fd(fd int) error {
	w.fds = append(w.fds, fd)
	return nil
}

// Bytes aliases the buffer; Reset or the next write invalidates it.
func (w *Writer) Bytes() []byte {
	return w.buf.Bytes()
}

// Fds aliases the slice; Reset drops it without closing anything.
func (w *Writer) Fds() []int {
	return w.fds
}

func (w *Writer) Reset() {
	w.buf.Reset()
	w.fds = w.fds[:0]
}
