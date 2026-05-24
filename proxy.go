package wayland

import (
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/xogas/wayland/wire"
)

type Proxy struct {
	id       uint32
	conn     *Conn
	deleted  atomic.Bool
	version  uint32
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
	return p.version
}

func (p *Proxy) SetVersion(v uint32) {
	p.version = v
}

// SetEventFDCounts sets the per-opcode file descriptor counts for incoming events.
func (p *Proxy) SetEventFDCounts(fdCounts map[uint16]int) {
	p.fdCounts = fdCounts
}

func (p *Proxy) fdCountForOpcode(opcode uint16) int {
	if p.fdCounts == nil {
		return 0
	}
	return p.fdCounts[opcode]
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
	handlers := make([]func(*wire.Reader), len(p.events[opcode]))
	copy(handlers, p.events[opcode])
	p.eventsMu.Unlock()

	hasFDs := p.fdCountForOpcode(opcode) > 0
	for _, h := range handlers {
		cr := r.Clone()
		h(cr)
		if hasFDs {
			for _, fd := range cr.UnconsumedFDs() {
				_ = syscall.Close(fd)
			}
		}
	}
}
