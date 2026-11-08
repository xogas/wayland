package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"testing"
)

func socketPair(t *testing.T) (a, b *net.UnixConn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	a = fileConn(t, fds[0])
	b = fileConn(t, fds[1])
	t.Cleanup(func() {
		_ = a.Close()
		_ = b.Close()
	})
	return a, b
}

func fileConn(t *testing.T, fd int) *net.UnixConn {
	t.Helper()
	f := os.NewFile(uintptr(fd), "wire-test")
	defer func() { _ = f.Close() }() // FileConn duplicates the fd
	c, err := net.FileConn(f)
	if err != nil {
		t.Fatalf("FileConn: %v", err)
	}
	return c.(*net.UnixConn)
}

func connPair(t *testing.T) (client, server *Conn) {
	t.Helper()
	a, b := socketPair(t)
	return NewConn(a), NewConn(b)
}

func tempFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "wire-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func openFDCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("cannot count open fds: %v", err)
	}
	return len(entries)
}

// rawFrame writes a header with an arbitrary length field, bypassing
// SendMessage's validation to simulate a malformed peer.
func rawFrame(t *testing.T, conn *net.UnixConn, obj uint32, length, opcode uint16, payload []byte) {
	t.Helper()
	buf := make([]byte, 8+len(payload))
	binary.NativeEndian.PutUint32(buf[0:4], obj)
	binary.NativeEndian.PutUint32(buf[4:8], uint32(length)<<16|uint32(opcode))
	copy(buf[8:], payload)
	if _, err := conn.Write(buf); err != nil {
		t.Fatalf("raw write: %v", err)
	}
}

// A correct peer cannot produce these. Accepting one shifts every following
// header by 0-3 bytes with no immediate error.
func TestReceiveRejectsImpossibleLengths(t *testing.T) {
	tests := []struct {
		name   string
		length uint16
	}{
		{"below header", 4},
		{"unaligned", 9},
		{"max but unaligned", 0xFFFF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server := socketPair(t)
			rawFrame(t, client, 1, tt.length, 0, make([]byte, 8))
			if _, _, _, err := NewConn(server).ReceiveMessage(); err == nil {
				t.Fatalf("accepted a length of %d", tt.length)
			}
		})
	}
}

func TestSplitFrameWaitsForPartialFrame(t *testing.T) {
	buf := make([]byte, 12)
	binary.NativeEndian.PutUint32(buf[0:4], 1)
	binary.NativeEndian.PutUint32(buf[4:8], 20<<16)
	_, rest, ok, err := splitFrame(buf)
	if ok || err != nil {
		t.Fatalf("partial frame: ok=%v err=%v", ok, err)
	}
	if len(rest) != len(buf) {
		t.Fatalf("partial frame consumed %d bytes", len(buf)-len(rest))
	}
}

// Both ends of the 16-bit length field.
func TestMessageSizeCeiling(t *testing.T) {
	client, server := connPair(t)

	w := &Writer{}
	if err := w.Array(make([]byte, 65520)); err != nil {
		t.Fatal(err)
	}
	if got := len(w.Bytes()); got != 65524 {
		t.Fatalf("payload = %d bytes, want 65524", got)
	}
	if err := client.SendMessage(1, 0, w); err != nil {
		t.Fatalf("65532-byte frame rejected: %v", err)
	}
	obj, opcode, r, err := server.ReceiveMessage()
	if err != nil {
		t.Fatalf("receiving the max frame: %v", err)
	}
	if obj != 1 || opcode != 0 || r.Remaining() != 65524 {
		t.Fatalf("max frame: obj=%d opcode=%d remaining=%d", obj, opcode, r.Remaining())
	}

	big := &Writer{}
	if err := big.Array(make([]byte, 65524)); err != nil {
		t.Fatal(err)
	}
	if err := client.SendMessage(2, 0, big); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("oversized send = %v, want ErrMessageTooLarge", err)
	}

	small := &Writer{}
	_ = small.Uint32(7)
	if err := client.SendMessage(3, 1, small); err != nil {
		t.Fatalf("follow-up send: %v", err)
	}
	if obj, opcode, _, err = server.ReceiveMessage(); err != nil || obj != 3 || opcode != 1 {
		t.Fatalf("follow-up frame: obj=%d opcode=%d err=%v", obj, opcode, err)
	}
}

func TestPartialReadKeepsFraming(t *testing.T) {
	client, server := connPair(t)

	payload := make([]byte, 20000)
	for i := range payload {
		payload[i] = byte(i)
	}
	w := &Writer{}
	if err := w.Array(payload); err != nil {
		t.Fatal(err)
	}
	if err := client.SendMessage(1, 0, w); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	_, _, r, err := server.ReceiveMessage()
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	got, err := r.Array()
	if err != nil {
		t.Fatalf("Array: %v", err)
	}
	if len(got) != len(payload) || got[len(got)-1] != payload[len(payload)-1] {
		t.Fatalf("payload mismatch: got %d bytes", len(got))
	}

	w2 := &Writer{}
	_ = w2.Uint32(42)
	if err := client.SendMessage(2, 1, w2); err != nil {
		t.Fatalf("follow-up send: %v", err)
	}
	if obj, opcode, r2, err := server.ReceiveMessage(); err != nil || obj != 2 || opcode != 1 {
		t.Fatalf("follow-up frame: obj=%d opcode=%d err=%v", obj, opcode, err)
	} else if v, err := r2.Uint32(); err != nil || v != 42 {
		t.Fatalf("follow-up payload = %d, %v", v, err)
	}
}

// Descriptors are not tagged with a message: queue order plus a dispatcher
// claiming exactly the declared count is all that keeps them paired.
func TestFDQueueAssignsInSendOrder(t *testing.T) {
	client, server := connPair(t)
	f1, f2 := tempFile(t), tempFile(t)

	w1 := &Writer{}
	_ = w1.Fd(int(f1.Fd()))
	if err := client.SendMessage(1, 0, w1); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	w2 := &Writer{}
	_ = w2.Fd(int(f2.Fd()))
	if err := client.SendMessage(2, 1, w2); err != nil {
		t.Fatalf("send 2: %v", err)
	}

	_, _, r1, err := server.ReceiveMessage()
	if err != nil {
		t.Fatalf("receive 1: %v", err)
	}
	_, _, r2, err := server.ReceiveMessage()
	if err != nil {
		t.Fatalf("receive 2: %v", err)
	}

	fd1 := server.TakeFDs(1)
	fd2 := server.TakeFDs(1)
	if len(fd1) != 1 || len(fd2) != 1 {
		t.Fatalf("TakeFDs = %v, %v", fd1, fd2)
	}
	r1.SetFDs(fd1)
	r2.SetFDs(fd2)

	fdA, err := r1.Fd()
	if err != nil {
		t.Fatalf("Fd from message 1: %v", err)
	}
	fdB, err := r2.Fd()
	if err != nil {
		t.Fatalf("Fd from message 2: %v", err)
	}
	assertInode(t, fdA, int(f1.Fd()))
	assertInode(t, fdB, int(f2.Fd()))
	_ = syscall.Close(fdA)
	_ = syscall.Close(fdB)
}

func assertInode(t *testing.T, gotFD, wantFD int) {
	t.Helper()
	var got, want syscall.Stat_t
	if err := syscall.Fstat(gotFD, &got); err != nil {
		t.Fatalf("fstat received fd: %v", err)
	}
	if err := syscall.Fstat(wantFD, &want); err != nil {
		t.Fatalf("fstat sent fd: %v", err)
	}
	if got.Ino != want.Ino {
		t.Fatalf("fd mix-up: got inode %d, want %d", got.Ino, want.Ino)
	}
}

// oobBuf holds maxFDsPerRead. The kernel closes what does not fit and installs
// the rest, which must be closed here.
func TestControlMessageTruncationClosesReceivedFDs(t *testing.T) {
	client, server := socketPair(t)
	conn := NewConn(server)

	const count = 40
	files := make([]*os.File, count)
	sent := make([]int, count)
	for i := range files {
		f, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatalf("open %s: %v", os.DevNull, err)
		}
		files[i] = f
		sent[i] = int(f.Fd())
	}
	t.Cleanup(func() {
		for _, f := range files {
			_ = f.Close()
		}
	})

	hdr := make([]byte, 8)
	binary.NativeEndian.PutUint32(hdr[0:4], 1)
	binary.NativeEndian.PutUint32(hdr[4:8], 8<<16)
	if _, _, err := client.WriteMsgUnix(hdr, syscall.UnixRights(sent...), nil); err != nil {
		t.Fatalf("WriteMsgUnix: %v", err)
	}
	for _, f := range files {
		_ = f.Close()
	}

	before := openFDCount(t)
	if _, _, _, err := conn.ReceiveMessage(); err == nil {
		t.Fatal("accepted a truncated control message")
	}
	if after := openFDCount(t); after != before {
		t.Fatalf("received descriptors leaked: %d open before, %d after", before, after)
	}
}

// An SCM_RIGHTS descriptor has no finalizers, so a Conn closed without
// dispatching leaks it until process exit.
func TestCloseClosesQueuedFDs(t *testing.T) {
	client, server := connPair(t)
	f := tempFile(t)

	w := &Writer{}
	_ = w.Fd(int(f.Fd()))
	if err := client.SendMessage(1, 0, w); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if _, _, _, err := server.ReceiveMessage(); err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	if len(server.fds) != 1 {
		t.Fatalf("queued fds = %d, want 1", len(server.fds))
	}
	fd := server.fds[0]

	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err == nil {
		t.Fatalf("queued fd %d survived Close", fd)
	}
}

func TestTakeFDsClampsAndDrains(t *testing.T) {
	c := &Conn{fds: []int{10, 20}}
	if got := c.TakeFDs(5); len(got) != 2 || got[0] != 10 || got[1] != 20 {
		t.Fatalf("TakeFDs(5) = %v, want [10 20]", got)
	}
	if got := c.TakeAllFDs(); len(got) != 0 {
		t.Fatalf("TakeAllFDs on a drained queue = %v, want empty", got)
	}
	if got := c.TakeFDs(1); len(got) != 0 {
		t.Fatalf("TakeFDs on a drained queue = %v, want empty", got)
	}
}

func TestCloseIsIdempotentAndStopsIO(t *testing.T) {
	client, server := connPair(t)
	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, _, _, err := server.ReceiveMessage(); err == nil {
		t.Fatal("ReceiveMessage succeeded after Close")
	}

	if err := client.Close(); err != nil {
		t.Fatalf("client Close: %v", err)
	}
	w := &Writer{}
	_ = w.Uint32(1)
	if err := client.SendMessage(1, 0, w); err == nil {
		t.Fatal("SendMessage succeeded after Close")
	}
}

func TestCloseDropsBufferedFrames(t *testing.T) {
	_, server := socketPair(t)
	conn := NewConn(server)

	// Close drains the descriptor queue, so returning this buffered frame
	// afterwards would decode it without descriptors.
	buf := make([]byte, 12)
	binary.NativeEndian.PutUint32(buf[0:4], 1)
	binary.NativeEndian.PutUint32(buf[4:8], 12<<16)
	conn.buf = buf

	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, _, _, err := conn.ReceiveMessage(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("ReceiveMessage after Close = %v, want net.ErrClosed", err)
	}
}

// SendMessage serializes whole frames; interleaved writes would produce
// invalid headers and split payloads.
func TestSendMessageConcurrentKeepsFraming(t *testing.T) {
	client, server := connPair(t)
	const writers, perWriter = 8, 64

	// Must run concurrently: a few hundred queued headers fill the send buffer
	// and deadlock the writers.
	received := make(chan error, 1)
	go func() {
		defer func() { _ = client.Close() }() // unblock writers if the stream breaks
		for range writers * perWriter {
			obj, opcode, r, err := server.ReceiveMessage()
			if err != nil {
				received <- err
				return
			}
			if obj != 1 || opcode != 0 {
				received <- fmt.Errorf("header: obj=%d opcode=%d", obj, opcode)
				return
			}
			if _, err := r.Uint32(); err != nil {
				received <- err
				return
			}
			if r.Remaining() != 0 {
				received <- fmt.Errorf("interleaved write: %d trailing bytes", r.Remaining())
				return
			}
		}
		received <- nil
	}()

	var wg sync.WaitGroup
	for id := range writers {
		wg.Go(func() {
			for seq := range perWriter {
				w := &Writer{}
				_ = w.Uint32(uint32(id<<16) | uint32(seq))
				if err := client.SendMessage(1, 0, w); err != nil {
					t.Errorf("SendMessage: %v", err)
					return
				}
			}
		})
	}
	wg.Wait()
	if err := <-received; err != nil {
		t.Fatalf("receiver: %v", err)
	}
}

// Reachable only if a field writer omits its padding.
func TestSendMessageRejectsUnalignedPayload(t *testing.T) {
	client, _ := connPair(t)
	w := &Writer{}
	_ = w.Uint32(1)
	_, _ = w.buf.WriteString("x") // bypass the aligned Writer API
	if err := client.SendMessage(1, 0, w); err == nil {
		t.Fatal("accepted an unaligned payload")
	}
}
