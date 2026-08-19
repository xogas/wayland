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
// tables. Generated code registers every interface from init; Registry.Bind
// looks the table up so raw proxies keep exact fd accounting.
var (
	interfaceFDCountsMu sync.RWMutex
	interfaceFDCounts   = map[string]map[uint16]int{}
)

// RegisterInterfaceFDCounts registers the per-opcode event fd-count table
// for a protocol interface name. Generated code calls this from init;
// binders of custom protocols should call it too for strict fd handling.
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
	// Borrow the handler slice under RLock: RegisterEvent is append-only, so
	// the slice stays valid for iteration without copying.
	p.eventsMu.RLock()
	handlers := p.events[opcode]
	p.eventsMu.RUnlock()

	totalFDs := len(r.UnconsumedFDs())
	maxConsumed := 0
	for _, h := range handlers {
		cr := r.Clone()
		h(cr)
		if consumed := totalFDs - len(cr.UnconsumedFDs()); consumed > maxConsumed {
			maxConsumed = consumed
		}
	}
	for _, fd := range r.UnconsumedFDs()[maxConsumed:] {
		_ = syscall.Close(fd)
	}
}
