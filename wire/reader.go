package wire

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Reader reads wire-format data from a byte slice and file descriptor queue.
type Reader struct {
	buf   []byte
	pos   int
	fds   []int
	fdIdx int
}

// NewReader creates a new Reader.
func NewReader(buf []byte, fds []int) *Reader {
	return &Reader{buf: buf, fds: fds}
}

// SetFDs assigns file descriptors to this Reader.
func (r *Reader) SetFDs(fds []int) {
	r.fds = fds
	r.fdIdx = 0
}

// UnconsumedFDs returns file descriptors that have not been read via Fd().
func (r *Reader) UnconsumedFDs() []int {
	return r.fds[r.fdIdx:]
}

// Clone returns a copy of the Reader with independent read position.
// The returned Reader shares the same underlying buffer and fd list.
func (r *Reader) Clone() *Reader {
	return &Reader{
		buf:   r.buf,
		pos:   r.pos,
		fds:   r.fds,
		fdIdx: r.fdIdx,
	}
}

func (r *Reader) check(n int) error {
	if r.pos+n > len(r.buf) {
		return fmt.Errorf("wire: read past end of buffer (need %d, have %d)", n, len(r.buf)-r.pos)
	}
	return nil
}

// Int32 reads a signed 32-bit integer in host byte order.
func (r *Reader) Int32() (int32, error) {
	if err := r.check(4); err != nil {
		return 0, err
	}
	v := int32(binary.NativeEndian.Uint32(r.buf[r.pos:]))
	r.pos += 4
	return v, nil
}

// Uint32 reads an unsigned 32-bit integer in host byte order.
func (r *Reader) Uint32() (uint32, error) {
	if err := r.check(4); err != nil {
		return 0, err
	}
	v := binary.NativeEndian.Uint32(r.buf[r.pos:])
	r.pos += 4
	return v, nil
}

// Fixed reads a 24.8 fixed-point number.
func (r *Reader) Fixed() (Fixed, error) {
	v, err := r.Int32()
	if err != nil {
		return 0, err
	}
	return Fixed(v), nil
}

// String reads a length-prefixed string.
// The length field includes the NUL terminator. A length of 0 means an empty string.
func (r *Reader) String() (string, error) {
	length, err := r.Uint32()
	if err != nil {
		return "", err
	}
	if length == 0 {
		return "", nil
	}
	byteLen := int(length) - 1
	if err := r.check(byteLen + 1); err != nil {
		return "", fmt.Errorf("wire: string data truncated (need %d): %w", byteLen+1, err)
	}
	s := string(r.buf[r.pos : r.pos+byteLen])
	r.pos += byteLen
	if r.buf[r.pos] != 0 {
		return "", fmt.Errorf("wire: missing NUL terminator in string")
	}
	r.pos++
	pad := (4 - int(length)%4) % 4
	if err := r.check(pad); err != nil {
		return "", fmt.Errorf("wire: string padding truncated: %w", err)
	}
	r.pos += pad
	return s, nil
}

// Object reads an object ID.
func (r *Reader) Object() (ObjectID, error) {
	v, err := r.Uint32()
	if err != nil {
		return 0, err
	}
	return ObjectID(v), nil
}

// NewID reads a new object ID.
func (r *Reader) NewID() (NewID, error) {
	v, err := r.Uint32()
	if err != nil {
		return 0, err
	}
	return NewID(v), nil
}

// Array reads a length-prefixed byte array.
func (r *Reader) Array() ([]byte, error) {
	length, err := r.Uint32()
	if err != nil {
		return nil, err
	}
	if err := r.check(int(length)); err != nil {
		return nil, fmt.Errorf("wire: array data truncated (need %d): %w", length, err)
	}
	v := make([]byte, length)
	copy(v, r.buf[r.pos:r.pos+int(length)])
	r.pos += int(length)
	pad := (4 - int(length)%4) % 4
	if err := r.check(pad); err != nil {
		return nil, fmt.Errorf("wire: array padding truncated: %w", err)
	}
	r.pos += pad
	return v, nil
}

// Fd returns the next file descriptor from the queue.
func (r *Reader) Fd() (int, error) {
	if r.fdIdx >= len(r.fds) {
		return 0, io.EOF
	}
	fd := r.fds[r.fdIdx]
	r.fdIdx++
	return fd, nil
}
