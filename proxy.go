package wayland

import (
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

// interfaceFDCounts maps protocol interface names to their event fd-count
// tables. Generated code registers every interface it defines from init;
// Registry.Bind looks the table up so that raw proxies are as safe as
// generated bindings. Without a table, fds carried by events on a raw proxy
// would be left stuck in the connection-level queue, skewing every
// subsequent fd-carrying message.
var (
	interfaceFDCountsMu sync.RWMutex
	interfaceFDCounts   = map[string]map[uint16]int{}
)

// RegisterInterfaceFDCounts registers the per-opcode event fd-count table for
// a protocol interface name. Generated code calls this from init; code that
// binds custom protocols through Registry.Bind and handles events manually
// should call it too, so dispatch can drain fds and reject unknown opcodes.
func RegisterInterfaceFDCounts(name string, counts map[uint16]int) {
	interfaceFDCountsMu.Lock()
	interfaceFDCounts[name] = counts
	interfaceFDCountsMu.Unlock()
}

func lookupInterfaceFDCounts(name string) (map[uint16]int, bool) {
	interfaceFDCountsMu.RLock()
	m, ok := interfaceFDCounts[name]
	interfaceFDCountsMu.RUnlock()
	return m, ok
}

type Proxy struct {
	id       uint32
	conn     *Conn
	deleted  atomic.Bool
	version  atomic.Uint32
	events   map[uint16][]func(*wire.Reader)
	eventsMu sync.RWMutex
	fdCounts atomic.Pointer[map[uint16]int]
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

// SetEventFDCounts sets the per-opcode file descriptor counts for incoming
// events. Safe for concurrent use with dispatch.
func (p *Proxy) SetEventFDCounts(fdCounts map[uint16]int) {
	p.fdCounts.Store(&fdCounts)
}

func (p *Proxy) hasEvent(opcode uint16) bool {
	p.eventsMu.RLock()
	defer p.eventsMu.RUnlock()
	return len(p.events[opcode]) > 0
}

func (p *Proxy) FDCounts() map[uint16]int {
	m := p.fdCounts.Load()
	if m == nil {
		return nil
	}
	return *m
}

func (p *Proxy) SendRequest(opcode uint16, m wire.Marshaler) error {
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
	// Borrow the handler slice under RLock instead of copying it. Handlers
	// are append-only (RegisterEvent); a concurrent append either reallocates
	// the backing array or writes beyond this slice's length, so iteration
	// sees exactly the handlers registered up to this point.
	p.eventsMu.RLock()
	handlers := p.events[opcode]
	p.eventsMu.RUnlock()

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
