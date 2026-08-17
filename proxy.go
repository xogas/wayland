package wayland

import (
	"slices"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/xogas/wayland/wire"
)

// Binder is implemented by objects that can bind a Wayland protocol interface
// to a server-side global. The generated BindXxx functions accept a Binder
// so they can be used with any client-side proxy that supports binding.
type Binder interface {
	Bind(name uint32, iface string, version uint32) (*Proxy, error)
}

type Proxy struct {
	id       uint32
	conn     *Conn
	deleted  atomic.Bool
	version  atomic.Uint32
	events   map[uint16][]func(*wire.Reader)
	eventsMu sync.Mutex
	fdCounts map[uint16]int
}

func NewProxy(conn *Conn) *Proxy {
	id := conn.allocID()
	return &Proxy{
		id:     id,
		conn:   conn,
		events: make(map[uint16][]func(*wire.Reader)),
	}
}

func NewProxyWithID(conn *Conn, id uint32) *Proxy {
	return &Proxy{
		id:     id,
		conn:   conn,
		events: make(map[uint16][]func(*wire.Reader)),
	}
}

func (p *Proxy) ID() uint32 {
	return p.id
}

func (p *Proxy) Conn() *Conn {
	return p.conn
}

func (p *Proxy) Deleted() bool {
	return p.deleted.Load()
}

func (p *Proxy) Version() uint32 {
	return p.version.Load()
}

func (p *Proxy) SetVersion(v uint32) {
	p.version.Store(v)
}

// SetEventFDCounts sets the per-opcode file descriptor counts for incoming events.
func (p *Proxy) SetEventFDCounts(fdCounts map[uint16]int) {
	p.fdCounts = fdCounts
}

// fdCountForOpcode returns the number of fds carried by an event opcode and
// whether the opcode exists in the interface at all. A nil table means the
// interface's event set is unknown (raw proxies); callers must then fall back
// to lenient handling because the fd count of an event cannot be determined.
func (p *Proxy) fdCountForOpcode(opcode uint16) (n int, known bool) {
	if p.fdCounts == nil {
		return 0, false
	}
	n, known = p.fdCounts[opcode]
	return n, known
}

func (p *Proxy) hasEvent(opcode uint16) bool {
	p.eventsMu.Lock()
	defer p.eventsMu.Unlock()
	return len(p.events[opcode]) > 0
}

func (p *Proxy) FDCounts() map[uint16]int {
	return p.fdCounts
}

func (p *Proxy) SendRequest(opcode uint16, m wire.Marshaler) error {
	if p.conn.IsClosed() {
		return ErrConnClosed
	}
	if p.deleted.Load() {
		return ErrObjectDeleted
	}
	return p.conn.SendRequest(p.id, opcode, m)
}

func (p *Proxy) RegisterEvent(opcode uint16, h func(*wire.Reader)) {
	p.eventsMu.Lock()
	p.events[opcode] = append(p.events[opcode], h)
	p.eventsMu.Unlock()
}

func (p *Proxy) dispatchEvent(opcode uint16, r *wire.Reader) {
	p.eventsMu.Lock()
	handlers := slices.Clone(p.events[opcode])
	p.eventsMu.Unlock()

	totalFDs := len(r.UnconsumedFDs())
	maxConsumed := 0
	for _, h := range handlers {
		cr := r.Clone()
		h(cr)
		if totalFDs > 0 {
			consumed := totalFDs - len(cr.UnconsumedFDs())
			if consumed > maxConsumed {
				maxConsumed = consumed
			}
		}
	}
	for _, fd := range r.UnconsumedFDs()[maxConsumed:] {
		_ = syscall.Close(fd)
	}
}
