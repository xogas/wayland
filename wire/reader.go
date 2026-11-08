package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
)

// ErrNoFD means the peer sent fewer descriptors than the interface declares.
// Not io.EOF: the queue is not a stream.
var ErrNoFD = errors.New("wire: no file descriptor left")

// Reader decodes one payload.
type Reader struct {
	buf   []byte
	pos   int
	fds   []int
	fdIdx int
}

// NewReader wraps buf without copying; buf must stay valid and unmodified.
func NewReader(buf []byte, fds []int) *Reader {
	return &Reader{buf: buf, fds: fds}
}

// SetFDs leaves pos untouched: bytes and fds are independent cursors.
func (r *Reader) SetFDs(fds []int) {
	r.fds = fds
	r.fdIdx = 0
}

func (r *Reader) UnconsumedFDs() []int {
	return r.fds[r.fdIdx:]
}

// Remaining is non-zero after a decode when the peer sent fields this binding
// lacks. Short payloads error inside the decode; extra ones do not.
func (r *Reader) Remaining() int {
	return len(r.buf) - r.pos
}

// Clone shares buf and fds; only the cursors diverge.
func (r *Reader) Clone() *Reader {
	c := *r
	return &c
}

func (r *Reader) Int32() (int32, error) {
	v, err := r.Uint32()
	return int32(v), err
}

func (r *Reader) Uint32() (uint32, error) {
	if err := r.check(4); err != nil {
		return 0, err
	}
	v := binary.NativeEndian.Uint32(r.buf[r.pos:])
	r.pos += 4
	return v, nil
}

func (r *Reader) Fixed() (Fixed, error) {
	v, err := r.Uint32()
	return Fixed(int32(v)), err
}

// String rejects NULL: only allowNull arguments carry it, and those go through
// StringNullable.
func (r *Reader) String() (string, error) {
	length, err := r.Uint32()
	if err != nil {
		return "", err
	}
	if length == 0 {
		return "", errors.New("wire: NULL for a non-nullable string")
	}
	return r.stringBody(length)
}

// StringNullable decodes NULL as nil and a present empty string as "".
func (r *Reader) StringNullable() (*string, error) {
	length, err := r.Uint32()
	if err != nil {
		return nil, err
	}
	if length == 0 {
		return nil, nil
	}
	s, err := r.stringBody(length)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *Reader) stringBody(length uint32) (string, error) {
	if length > maxPayload {
		return "", fmt.Errorf("wire: string length %d exceeds frame payload", length)
	}
	n := int(length) - 1 // data bytes, NUL excluded; length >= 1 here
	if err := r.check(n + 1); err != nil {
		return "", fmt.Errorf("wire: string truncated (need %d): %w", n+1, err)
	}
	s := string(r.buf[r.pos : r.pos+n])
	r.pos += n
	if r.buf[r.pos] != 0 {
		return "", errors.New("wire: string missing NUL terminator")
	}
	r.pos++
	return s, r.skipPad(int(length))
}

func (r *Reader) Object() (ObjectID, error) {
	v, err := r.Uint32()
	return ObjectID(v), err
}

func (r *Reader) NewID() (NewID, error) {
	v, err := r.Uint32()
	return NewID(v), err
}

// Length 0 yields a non-nil empty slice.
func (r *Reader) Array() ([]byte, error) {
	length, err := r.Uint32()
	if err != nil {
		return nil, err
	}
	if length > maxPayload {
		return nil, fmt.Errorf("wire: array length %d exceeds frame payload", length)
	}
	n := int(length)
	if err := r.check(n); err != nil {
		return nil, fmt.Errorf("wire: array truncated (need %d): %w", n, err)
	}
	v := slices.Clone(r.buf[r.pos : r.pos+n])
	r.pos += n
	if err := r.skipPad(n); err != nil {
		return nil, err
	}
	return v, nil
}

func (r *Reader) Fd() (int, error) {
	if r.fdIdx >= len(r.fds) {
		return 0, ErrNoFD
	}
	fd := r.fds[r.fdIdx]
	r.fdIdx++
	return fd, nil
}

// check needs both tests: prefixes are peer-chosen uint32, so on 32-bit
// int(prefix) is negative for the top half, and pos+n can overflow where the
// subtraction cannot.
func (r *Reader) check(n int) error {
	if n < 0 || n > len(r.buf)-r.pos {
		return fmt.Errorf("wire: read past end of buffer (need %d, have %d)", n, len(r.buf)-r.pos)
	}
	return nil
}

func (r *Reader) skipPad(length int) error {
	pad := (4 - length%4) % 4
	if err := r.check(pad); err != nil {
		return fmt.Errorf("wire: padding truncated: %w", err)
	}
	r.pos += pad
	return nil
}
