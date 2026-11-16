package wayland

import (
	"encoding/binary"
	"fmt"
	"syscall"
)

func closeArgFDs(args []Arg) {
	for _, arg := range args {
		if arg.typ == ArgFD && arg.fd >= 0 {
			_ = syscall.Close(arg.fd)
		}
	}
}

func closeOOBFds(oob []byte) {
	if len(oob) == 0 {
		return
	}
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return
	}
	for i := range msgs {
		rights, err := syscall.ParseUnixRights(&msgs[i])
		if err != nil {
			continue
		}
		closeFDs(rights)
	}
}

func encodeRequest(p *Proxy, opcode uint16, spec Message, args []Arg) ([]byte, []int, error) {
	if p == nil || p.iface == nil {
		return nil, nil, fmt.Errorf("encode: nil object: %w", ErrInvalidArgument)
	}
	if len(args) != len(spec.Args) {
		return nil, nil, fmt.Errorf("encode %s.%s: got %d args, want %d: %w", p.iface.Name, spec.Name, len(args), len(spec.Args), ErrInvalidArgument)
	}
	body := make([]byte, 0, 64)
	var fds []int
	for i, as := range spec.Args {
		arg := args[i]
		switch as.Type {
		case ArgInt:
			if arg.typ != ArgInt {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not int: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			body = appendU32(body, uint32(arg.i32))
		case ArgUint:
			if arg.typ != ArgUint {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not uint: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			body = appendU32(body, arg.u32)
		case ArgFixed:
			if arg.typ != ArgFixed {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not fixed: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			body = appendU32(body, uint32(arg.fixedRaw()))
		case ArgString:
			if arg.typ != ArgString {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not string: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			if arg.str == nil {
				if !as.AllowNull {
					return nil, nil, fmt.Errorf("encode %s.%s.%s: null string not allowed: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
				}
				body = appendU32(body, 0)
				break
			}
			body = appendStringField(body, *arg.str)
		case ArgArray:
			if arg.typ != ArgArray {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not array: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			if arg.bytes == nil {
				if !as.AllowNull {
					return nil, nil, fmt.Errorf("encode %s.%s.%s: null array not allowed: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
				}
				body = appendU32(body, 0)
				break
			}
			body = appendU32(body, uint32(len(arg.bytes)))
			body = append(body, arg.bytes...)
			body = pad4(body)
		case ArgFD:
			if arg.typ != ArgFD {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not fd: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			if !validFD(arg.fd) {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: invalid fd %d: %w", p.iface.Name, spec.Name, as.Name, arg.fd, ErrInvalidArgument)
			}
			fds = append(fds, arg.fd)
		case ArgNewID:
			if arg.typ != ArgNewID {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not new_id: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			if arg.u32 == 0 && !as.AllowNull {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: null new_id not allowed: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			if as.Interface == "" {
				// wl_registry.bind uses a dynamic new_id. The wire format
				// carries the requested interface name and version in
				// addition to the object ID even though the frozen XML
				// signature only names the new_id.
				if arg.u32 == 0 {
					body = appendStringField(body, "")
					body = appendU32(body, 0)
					body = appendU32(body, 0)
					break
				}
				target, err := dynamicNewIDTarget(p, arg.u32)
				if err != nil {
					return nil, nil, fmt.Errorf("encode %s.%s.%s: %w", p.iface.Name, spec.Name, as.Name, err)
				}
				body = appendStringField(body, target.iface.Name)
				body = appendU32(body, target.version)
			}
			body = appendU32(body, arg.u32)
		case ArgObject:
			if arg.typ != ArgObject {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: not object: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			if arg.u32 == 0 && !as.AllowNull {
				return nil, nil, fmt.Errorf("encode %s.%s.%s: null object not allowed: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
			}
			body = appendU32(body, arg.u32)
		default:
			return nil, nil, fmt.Errorf("encode %s.%s.%s: unknown arg type: %w", p.iface.Name, spec.Name, as.Name, ErrInvalidArgument)
		}
	}
	size := 8 + len(body)
	if size > messageMax {
		return nil, nil, fmt.Errorf("encode %s.%s: size %d: %w", p.iface.Name, spec.Name, size, ErrMalformedMessage)
	}
	data := make([]byte, 8, size)
	binary.LittleEndian.PutUint32(data[0:4], p.id)
	binary.LittleEndian.PutUint32(data[4:8], uint32(size)<<16|uint32(opcode))
	data = append(data, body...)
	return data, fds, nil
}

// validFD reports whether fd refers to an open descriptor in this process.
// Only the standard library is available, so this uses fstat(2).
func validFD(fd int) bool {
	if fd < 0 {
		return false
	}
	var st syscall.Stat_t
	return syscall.Fstat(fd, &st) == nil
}

func appendStringField(body []byte, s string) []byte {
	body = appendU32(body, uint32(len(s)+1))
	body = append(body, s...)
	body = append(body, 0)
	return pad4(body)
}

// dynamicNewIDTarget resolves the interface and version of a dynamic new_id
// from the client object that was allocated for it.
func dynamicNewIDTarget(p *Proxy, id uint32) (*Proxy, error) {
	if p == nil || p.conn == nil {
		return nil, fmt.Errorf("dynamic new_id %d: no connection: %w", id, ErrInvalidArgument)
	}
	target := p.conn.lookup(id)
	if target == nil || target.iface == nil {
		return nil, fmt.Errorf("dynamic new_id %d: object not registered: %w", id, ErrInvalidArgument)
	}
	if target.version < 1 || target.version > target.iface.Version {
		return nil, fmt.Errorf("dynamic new_id %d: %s v%d: %w", id, target.iface.Name, target.version, ErrVersion)
	}
	return target, nil
}

func (c *conn) decodeArgs(body []byte, specs []ArgSpec, parent *Proxy) ([]Arg, error) {
	off := 0
	readU32 := func() (uint32, error) {
		if off+4 > len(body) {
			return 0, fmt.Errorf("decode: truncated uint: %w", ErrMalformedMessage)
		}
		v := binary.LittleEndian.Uint32(body[off : off+4])
		off += 4
		return v, nil
	}
	readStringField := func() (string, error) {
		n, err := readU32()
		if err != nil {
			return "", err
		}
		if n == 0 {
			return "", nil
		}
		if int(n) > len(body)-off || n < 1 {
			return "", fmt.Errorf("decode string length %d: %w", n, ErrMalformedMessage)
		}
		raw := body[off : off+int(n)]
		if raw[len(raw)-1] != 0 {
			return "", fmt.Errorf("decode string: missing NUL: %w", ErrMalformedMessage)
		}
		s := string(raw[:len(raw)-1])
		off += int(n)
		off = align4(off)
		return s, nil
	}
	args := make([]Arg, 0, len(specs))
	ok := false
	defer func() {
		if !ok {
			closeArgFDs(args)
		}
	}()
	for _, as := range specs {
		switch as.Type {
		case ArgInt:
			v, err := readU32()
			if err != nil {
				return nil, err
			}
			args = append(args, Int(int32(v)))
		case ArgUint:
			v, err := readU32()
			if err != nil {
				return nil, err
			}
			args = append(args, Uint(v))
		case ArgFixed:
			v, err := readU32()
			if err != nil {
				return nil, err
			}
			args = append(args, Arg{typ: ArgFixed, f64: float64(int32(v)) / 256})
		case ArgString:
			n, err := readU32()
			if err != nil {
				return nil, err
			}
			if n == 0 {
				if !as.AllowNull {
					return nil, fmt.Errorf("decode %s: null string not allowed: %w", as.Name, ErrInvalidArgument)
				}
				args = append(args, String(nil))
				break
			}
			if int(n) > len(body)-off || n < 1 {
				return nil, fmt.Errorf("decode string length %d: %w", n, ErrMalformedMessage)
			}
			raw := body[off : off+int(n)]
			if raw[len(raw)-1] != 0 {
				return nil, fmt.Errorf("decode string: missing NUL: %w", ErrMalformedMessage)
			}
			s := string(raw[:len(raw)-1])
			off += int(n)
			off = align4(off)
			args = append(args, String(&s))
		case ArgArray:
			n, err := readU32()
			if err != nil {
				return nil, err
			}
			if n == 0 {
				// A zero-length array is indistinguishable from a null
				// array on the wire; the protocol treats it as empty.
				args = append(args, Array(nil))
				break
			}
			if int(n) > len(body)-off {
				return nil, fmt.Errorf("decode array length %d: %w", n, ErrMalformedMessage)
			}
			b := append([]byte(nil), body[off:off+int(n)]...)
			off += int(n)
			off = align4(off)
			args = append(args, Array(b))
		case ArgFD:
			fd, err := c.takeFD()
			if err != nil {
				return nil, err
			}
			args = append(args, FD(fd))
		case ArgNewID:
			if as.Interface == "" {
				// Dynamic new_id (wl_registry.bind): the wire carries the
				// requested interface name, its version and the object ID.
				ifaceName, err := readStringField()
				if err != nil {
					return nil, err
				}
				version, err := readU32()
				if err != nil {
					return nil, err
				}
				id, err := readU32()
				if err != nil {
					return nil, err
				}
				if id == 0 {
					if !as.AllowNull {
						return nil, fmt.Errorf("decode %s: null new_id not allowed: %w", as.Name, ErrInvalidArgument)
					}
					args = append(args, NewID(0))
					break
				}
				iface, okInterface := InterfaceByName(ifaceName)
				if !okInterface || version < 1 || version > iface.Version {
					return nil, fmt.Errorf("decode dynamic new_id %q v%d: %w", ifaceName, version, ErrInvalidArgument)
				}
				p, err := c.newIncomingObject(iface, version, id)
				if err != nil {
					return nil, err
				}
				args = append(args, Arg{typ: ArgNewID, u32: id, proxy: p})
				break
			}
			id, err := readU32()
			if err != nil {
				return nil, err
			}
			if id == 0 {
				if !as.AllowNull {
					return nil, fmt.Errorf("decode %s: null new_id not allowed: %w", as.Name, ErrInvalidArgument)
				}
				args = append(args, NewID(0))
				break
			}
			iface, okInterface := InterfaceByName(as.Interface)
			if !okInterface {
				return nil, fmt.Errorf("decode new_id interface %q: %w", as.Interface, ErrProtocolViolation)
			}
			version := parent.version
			if iface.Version < version {
				version = iface.Version
			}
			p, err := c.newIncomingObject(iface, version, id)
			if err != nil {
				return nil, err
			}
			args = append(args, Arg{typ: ArgNewID, u32: id, proxy: p})
		case ArgObject:
			id, err := readU32()
			if err != nil {
				return nil, err
			}
			if id == 0 {
				if !as.AllowNull {
					return nil, fmt.Errorf("decode %s: null object not allowed: %w", as.Name, ErrInvalidArgument)
				}
				args = append(args, Object(0))
				break
			}
			p := c.lookup(id)
			if p == nil {
				return nil, fmt.Errorf("decode object %d: %w", id, ErrProtocolViolation)
			}
			args = append(args, Arg{typ: ArgObject, u32: id, proxy: p})
		default:
			return nil, fmt.Errorf("decode %s: unknown arg type: %w", as.Name, ErrMalformedMessage)
		}
	}
	if off != len(body) {
		return nil, fmt.Errorf("decode: %d trailing bytes: %w", len(body)-off, ErrMalformedMessage)
	}
	ok = true
	return args, nil
}

func appendU32(b []byte, v uint32) []byte {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	return append(b, tmp[:]...)
}

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func align4(n int) int {
	if n%4 == 0 {
		return n
	}
	return n + (4 - n%4)
}
