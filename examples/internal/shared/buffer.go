package shared

import (
	"fmt"
	"os"
	"syscall"

	"github.com/xogas/wayland"
	"github.com/xogas/wayland/wire"
)

// ShmFile creates an anonymous shared-memory file of the given size and
// returns its fd plus a function that closes it.
func ShmFile(size int64) (fd int, closeFn func(), err error) {
	f, err := os.CreateTemp("", "wayland-shm-*")
	if err != nil {
		return 0, nil, err
	}
	_ = os.Remove(f.Name())
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		return 0, nil, err
	}
	return int(f.Fd()), func() { _ = f.Close() }, nil
}

// DoubleBuffer is a two-slot shm buffer pool with release tracking. The
// compositor releases a slot once it is done with it; Next / Free then return
// that slot for redrawing.
type DoubleBuffer struct {
	W, H, Stride int32
	IDs          [2]wire.ObjectID
	Pixels       [2][]byte

	free chan int
	bufs [2]*wayland.Buffer
	pool *wayland.ShmPool
	data []byte
	fd   int
	clFD func()
}

// NewDoubleBuffer allocates two w×h XRGB8888 buffers in one shm pool. Both
// slots are initially free.
func NewDoubleBuffer(shm *wayland.Shm, w, h int32) (*DoubleBuffer, error) {
	stride := w * 4
	oneSize := int64(h) * int64(stride)
	poolSize := oneSize * 2

	fd, closeFd, err := ShmFile(poolSize)
	if err != nil {
		return nil, fmt.Errorf("shm: %w", err)
	}
	data, err := syscall.Mmap(fd, 0, int(poolSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		closeFd()
		return nil, fmt.Errorf("mmap: %w", err)
	}
	pool, err := shm.CreatePool(fd, int32(poolSize))
	if err != nil {
		_ = syscall.Munmap(data)
		closeFd()
		return nil, fmt.Errorf("create_pool: %w", err)
	}

	db := &DoubleBuffer{
		W:      w,
		H:      h,
		Stride: stride,
		free:   make(chan int, 2),
		pool:   pool,
		data:   data,
		fd:     fd,
		clFD:   closeFd,
	}
	for i := 0; i < 2; i++ {
		off := int32(i) * int32(oneSize)
		buf, err := pool.CreateBuffer(off, w, h, stride, wayland.ShmFormatXrgb8888)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("create_buffer %d: %w", i, err)
		}
		db.IDs[i] = wire.ObjectID(buf.Proxy().ID())
		db.Pixels[i] = data[off : off+int32(oneSize)]
		db.bufs[i] = buf
		buf.OnRelease(func(ev wayland.BufferReleaseEvent) {
			db.free <- i
		})
	}
	db.free <- 0
	db.free <- 1
	return db, nil
}

// Next blocks until a slot is free and returns its index.
func (db *DoubleBuffer) Next() int {
	return <-db.free
}

// Free returns the channel of released slot indices, for select-based loops.
func (db *DoubleBuffer) Free() <-chan int {
	return db.free
}

// Close releases the buffers, pool, mapping and shm file.
func (db *DoubleBuffer) Close() {
	for _, buf := range db.bufs {
		_ = buf.Destroy()
	}
	_ = db.pool.Destroy()
	_ = syscall.Munmap(db.data)
	db.clFD()
}

// NewBuffer allocates a single w×h shm buffer and returns its object id,
// pixel data and a cleanup function. The buffer is not attached anywhere.
func NewBuffer(shm *wayland.Shm, w, h int32, format wayland.ShmFormat) (id wire.ObjectID, pixels []byte, cleanup func(), err error) {
	stride := w * 4
	bufSize := int64(h) * int64(stride)
	fd, closeFd, err := ShmFile(bufSize)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("shm: %w", err)
	}
	data, err := syscall.Mmap(fd, 0, int(bufSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		closeFd()
		return 0, nil, nil, fmt.Errorf("mmap: %w", err)
	}
	pool, err := shm.CreatePool(fd, int32(bufSize))
	if err != nil {
		_ = syscall.Munmap(data)
		closeFd()
		return 0, nil, nil, fmt.Errorf("create_pool: %w", err)
	}
	buf, err := pool.CreateBuffer(0, w, h, stride, format)
	if err != nil {
		_ = pool.Destroy()
		_ = syscall.Munmap(data)
		closeFd()
		return 0, nil, nil, fmt.Errorf("create_buffer: %w", err)
	}
	return wire.ObjectID(buf.Proxy().ID()), data,
		func() { _ = buf.Destroy(); _ = pool.Destroy(); _ = syscall.Munmap(data); closeFd() },
		nil
}
