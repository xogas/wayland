package wayland

import (
	"log/slog"
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
	idCounter  uint64
	closed     atomic.Bool
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
	if c.closed.Load() {
		return ErrConnClosed
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

func (c *Conn) UnregisterProxy(id uint32) {
	c.objectsMu.Lock()
	if p, ok := c.objects[id]; ok {
		p.deleted.Store(true)
		if fdCounts := p.FDCounts(); fdCounts != nil {
			zombie := make(map[uint16]int, len(fdCounts))
			for k, v := range fdCounts {
				zombie[k] = v
			}
			c.zombies[id] = zombie
		}
		delete(c.objects, id)
	}
	c.objectsMu.Unlock()
}

func (c *Conn) LookupProxy(id uint32) *Proxy {
	c.objectsMu.RLock()
	p := c.objects[id]
	c.objectsMu.RUnlock()
	return p
}

func (c *Conn) allocID() uint32 {
	v := atomic.AddUint64(&c.idCounter, 1)
	return uint32(v + 1)
}

func (c *Conn) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	close(c.done)
	if c.readCh != nil {
		for {
			select {
			case res := <-c.readCh:
				if res.r != nil {
					for _, fd := range res.r.UnconsumedFDs() {
						_ = syscall.Close(fd)
					}
				}
			default:
				goto drained
			}
		}
	}
drained:
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
