package wayland

import (
	"fmt"
	"log/slog"
	"maps"
	"net"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/xogas/wayland/wire"
)

type Conn struct {
	wc         *wire.Conn
	uc         *net.UnixConn
	sendMu     sync.Mutex
	objects    map[uint32]*Proxy
	objectsMu  sync.RWMutex
	zombies    map[uint32]map[uint16]int
	idCounter  atomic.Uint32
	closed     atomic.Bool
	errMu      sync.Mutex
	readErr    error
	protoErr   error
	connMu     sync.Mutex
	logger     *slog.Logger
	onError    func(*ProtocolError)
	readerOnce sync.Once
	readCh     chan readResult
	done       chan struct{}
}

func newConn(uc *net.UnixConn, wc *wire.Conn) *Conn {
	return &Conn{
		wc:      wc,
		uc:      uc,
		objects: make(map[uint32]*Proxy),
		zombies: make(map[uint32]map[uint16]int),
		logger:  slog.Default(),
		readCh:  make(chan readResult, 16),
		done:    make(chan struct{}),
	}
}

func (c *Conn) SetLogger(l *slog.Logger) {
	if l != nil {
		c.connMu.Lock()
		c.logger = l
		c.connMu.Unlock()
	}
}

func (c *Conn) Logger() *slog.Logger {
	c.connMu.Lock()
	l := c.logger
	c.connMu.Unlock()
	return l
}

func (c *Conn) SendRequest(objID uint32, opcode uint16, m wire.Marshaler) error {
	if err := c.stickyErr(); err != nil {
		return err
	}
	w := &wire.Writer{}
	if err := m.Marshal(w); err != nil {
		return err
	}
	c.sendMu.Lock()
	err := c.wc.SendMessage(wire.ObjectID(objID), opcode, w)
	c.sendMu.Unlock()
	return err
}

func (c *Conn) RegisterProxy(p *Proxy) {
	c.objectsMu.Lock()
	c.objects[p.id] = p
	c.objectsMu.Unlock()
}

func (c *Conn) RegisterProxyWithID(p *Proxy, id uint32) {
	p.id = id
	c.RegisterProxy(p)
}

// UnregisterProxy removes a proxy destroyed by the client. If the object had
// fd-carrying events, a zombie entry is kept so that events already in flight
// can have their fds drained and closed. The zombie lives until the server
// confirms the destruction with wl_display.delete_id (see removeProxy).
func (c *Conn) UnregisterProxy(id uint32) {
	c.objectsMu.Lock()
	if p, ok := c.objects[id]; ok {
		p.deleted.Store(true)
		if fdCounts := p.FDCounts(); fdCounts != nil {
			c.zombies[id] = maps.Clone(fdCounts)
		}
		delete(c.objects, id)
	}
	c.objectsMu.Unlock()
}

// removeProxy handles wl_display.delete_id: the object is gone for good and
// the protocol guarantees no further events will reference it, so any zombie
// entry left by a client-side destroy is dropped as well.
func (c *Conn) removeProxy(id uint32) {
	c.objectsMu.Lock()
	if p, ok := c.objects[id]; ok {
		p.deleted.Store(true)
		delete(c.objects, id)
	}
	delete(c.zombies, id)
	c.objectsMu.Unlock()
}

func (c *Conn) LookupProxy(id uint32) *Proxy {
	c.objectsMu.RLock()
	p := c.objects[id]
	c.objectsMu.RUnlock()
	return p
}

func (c *Conn) allocID() uint32 {
	return c.idCounter.Add(1) + 1
}

// setReadErr records the first fatal read error. It is sticky: once set, all
// future Dispatch, DispatchPending and SendRequest calls fail fast instead of
// blocking on a reader goroutine that no longer exists.
func (c *Conn) setReadErr(err error) {
	c.errMu.Lock()
	if c.readErr == nil {
		c.readErr = err
	}
	c.errMu.Unlock()
}

// setProtoErr records the first protocol-level failure: a wl_display.error
// event or an event that could not be decoded from the wire. Once set, the
// connection is treated as dead: the compositor has declared the protocol
// state invalid (or the stream is corrupt), so continuing would only dispatch
// garbage. It is sticky and takes priority over ErrConnClosed so a protocol
// error is never masked by the auto-close that follows it.
func (c *Conn) setProtoErr(err error) {
	c.errMu.Lock()
	if c.protoErr == nil {
		c.protoErr = err
	}
	c.errMu.Unlock()
}

// FailEvent reports a fatal event decode failure and terminates the
// connection. It is called by generated event handlers when an event cannot
// be decoded from the wire: the byte stream can no longer be trusted, and
// continuing would misparse every subsequent event and desynchronize the
// connection-level fd queue. The failure surfaces to the application as the
// error returned by Dispatch / DispatchPending.
func (c *Conn) FailEvent(event string, err error) {
	c.connMu.Lock()
	logger := c.logger
	c.connMu.Unlock()
	logger.Error("event unmarshal error", "event", event, "error", err)
	c.setProtoErr(fmt.Errorf("wayland: decode event %s: %w", event, err))
	c.Close()
}

// stickyErr reports the connection's fatal state, in priority order: a
// protocol-level failure (wl_display.error or an event decode failure),
// ErrConnClosed after Close, then the first reader error.
func (c *Conn) stickyErr() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if c.protoErr != nil {
		return c.protoErr
	}
	if c.closed.Load() {
		return ErrConnClosed
	}
	return c.readErr
}

func (c *Conn) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	close(c.done)
	// Messages still queued in readCh hold no resources: fds stay in the
	// wire-level queue until dispatch assigns them to a message.
	for _, fd := range c.wc.TakeAllFDs() {
		_ = syscall.Close(fd)
	}
	c.objectsMu.Lock()
	for id, p := range c.objects {
		p.deleted.Store(true)
		delete(c.objects, id)
	}
	c.objectsMu.Unlock()
	return c.wc.Close()
}

func (c *Conn) IsClosed() bool {
	return c.closed.Load()
}
