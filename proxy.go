package wayland

import "fmt"

// EventSink receives decoded events for one object.
type EventSink interface {
	DispatchEvent(opcode uint16, args []Arg) error
}

// Global is one wl_registry.global entry.
type Global struct {
	Name      uint32
	Interface string
	Version   uint32
}

type objectState uint8

const (
	stateAlive objectState = iota
	stateDestroyPending
	stateDestroyed
)

// Proxy is the runtime handle behind a generated object.
type Proxy struct {
	conn        *conn
	id          uint32
	version     uint32
	iface       *Interface
	sink        EventSink
	state       objectState
	serverOwned bool
	sent        bool
	// tombstone marks a canceled Roundtrip callback. Its events are consumed
	// and discarded until the server sends delete_id.
	tombstone bool
	// rawName marks an object created by the test-only raw bind path for an
	// interface without generated metadata. It is empty for generated
	// objects. Events for a raw object are skipped by message length instead
	// of being decoded; this is not a public API.
	rawName string
	// skippedEvents counts raw-object events consumed by the length-based
	// skip path. Tests use it to prove an event was skipped rather than
	// decoded; it is not a public API.
	skippedEvents uint32
}

// ID returns the wire object ID.
func (p *Proxy) ID() uint32 { return p.id }

// Version returns the object version.
func (p *Proxy) Version() uint32 { return p.version }

// Interface returns the object interface metadata.
func (p *Proxy) Interface() *Interface { return p.iface }

// SetSink installs the event sink for an event-created object.
func (p *Proxy) SetSink(sink EventSink) error {
	if sink == nil {
		return fmt.Errorf("set sink: %w", ErrInvalidArgument)
	}
	p.conn.mu.Lock()
	defer p.conn.mu.Unlock()
	if p.sink != nil {
		return fmt.Errorf("set sink: %w", ErrInvalidOperation)
	}
	p.sink = sink
	return nil
}

// Send writes one request.
func (p *Proxy) Send(opcode uint16, args ...Arg) error {
	return p.conn.sendRequest(p, opcode, args)
}

// NewObject allocates a child object at min(parent version, interface version).
func (p *Proxy) NewObject(iface *Interface) (*Proxy, error) {
	version := p.version
	if iface.Version < version {
		version = iface.Version
	}
	return p.conn.newObject(iface, version, false)
}

func (p *Proxy) newObjectWithVersion(iface *Interface, version uint32) (*Proxy, error) {
	if version < 1 || version > iface.Version {
		return nil, fmt.Errorf("new %s v%d: %w", iface.Name, version, ErrVersion)
	}
	return p.conn.newObject(iface, version, false)
}

func (p *Proxy) markServerOwned() {
	p.conn.mu.Lock()
	p.serverOwned = true
	p.conn.mu.Unlock()
}

// Release frees an object that was allocated but never sent.
func (p *Proxy) Release() error {
	return p.conn.release(p)
}
