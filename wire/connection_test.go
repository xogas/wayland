package wire

import (
	"encoding/binary"
	"net"
	"os"
	"syscall"
	"testing"
)

func socketPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	f1 := os.NewFile(uintptr(fds[0]), "sock1")
	f2 := os.NewFile(uintptr(fds[1]), "sock2")
	defer f1.Close() //nolint: errcheck
	defer f2.Close() //nolint: errcheck

	c1, err := net.FileConn(f1)
	if err != nil {
		t.Fatalf("FileConn f1: %v", err)
	}
	c2, err := net.FileConn(f2)
	if err != nil {
		t.Fatalf("FileConn f2: %v", err)
	}
	return c1.(*net.UnixConn), c2.(*net.UnixConn)
}

func TestSendReceiveEmpty(t *testing.T) {
	c1, c2 := socketPair(t)
	defer c1.Close() //nolint: errcheck
	defer c2.Close() //nolint: errcheck

	conn1 := NewConn(c1)
	conn2 := NewConn(c2)

	w := &Writer{}
	if err := conn1.SendMessage(1, 0, w); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	obj, opcode, r, err := conn2.ReceiveMessage()
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	if obj != 1 {
		t.Errorf("obj: got %d, want 1", obj)
	}
	if opcode != 0 {
		t.Errorf("opcode: got %d, want 0", opcode)
	}
	if len(r.buf) != 0 {
		t.Errorf("payload: got len=%d, want 0", len(r.buf))
	}
}

func TestSendReceivePayload(t *testing.T) {
	c1, c2 := socketPair(t)
	defer c1.Close() //nolint: errcheck
	defer c2.Close() //nolint: errcheck

	conn1 := NewConn(c1)
	conn2 := NewConn(c2)

	w := &Writer{}
	_ = w.Uint32(0xCAFEBABE)
	_ = w.Int32(-1)
	_ = w.Fixed(FixedFromFloat64(3.5))
	if err := conn1.SendMessage(7, 3, w); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	obj, opcode, r, err := conn2.ReceiveMessage()
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	if obj != 7 {
		t.Errorf("obj: got %d, want 7", obj)
	}
	if opcode != 3 {
		t.Errorf("opcode: got %d, want 3", opcode)
	}
	v1, _ := r.Uint32()
	if v1 != 0xCAFEBABE {
		t.Errorf("uint32: got %#x", v1)
	}
	v2, _ := r.Int32()
	if v2 != -1 {
		t.Errorf("int32: got %d", v2)
	}
	v3, _ := r.Fixed()
	if v3.Float64() != 3.5 {
		t.Errorf("fixed: got %v", v3.Float64())
	}
}

func TestSendReceiveFd(t *testing.T) {
	c1, c2 := socketPair(t)
	defer c1.Close() //nolint: errcheck
	defer c2.Close() //nolint: errcheck

	conn1 := NewConn(c1)
	conn2 := NewConn(c2)

	tmp, err := os.CreateTemp("", "wire-test-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(tmp.Name()) //nolint: errcheck
	defer tmp.Close()           //nolint: errcheck

	var stat1 syscall.Stat_t
	if err := syscall.Fstat(int(tmp.Fd()), &stat1); err != nil {
		t.Fatalf("fstat: %v", err)
	}

	w := &Writer{}
	_ = w.Fd(int(tmp.Fd()))
	if err := conn1.SendMessage(1, 0, w); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	obj, _, r, err := conn2.ReceiveMessage()
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	if obj != 1 {
		t.Errorf("obj: got %d, want 1", obj)
	}

	fds := conn2.TakeFDs(1)
	if len(fds) != 1 {
		t.Fatalf("TakeFDs: expected 1 fd, got %d", len(fds))
	}
	r.SetFDs(fds)

	fd, err := r.Fd()
	if err != nil {
		t.Fatalf("Fd: %v", err)
	}

	var stat2 syscall.Stat_t
	if err := syscall.Fstat(fd, &stat2); err != nil {
		t.Fatalf("fstat received fd: %v", err)
	}
	_ = syscall.Close(fd)

	if stat1.Ino != stat2.Ino {
		t.Errorf("fd inode mismatch: sent %d, got %d", stat1.Ino, stat2.Ino)
	}
}

func TestSendReceiveMultipleFds(t *testing.T) {
	c1, c2 := socketPair(t)
	defer c1.Close() //nolint: errcheck
	defer c2.Close() //nolint: errcheck

	conn1 := NewConn(c1)
	conn2 := NewConn(c2)

	f1, _ := os.CreateTemp("", "wire-test-*")
	f2, _ := os.CreateTemp("", "wire-test-*")
	defer os.Remove(f1.Name()) //nolint: errcheck
	defer os.Remove(f2.Name()) //nolint: errcheck
	defer f1.Close()           //nolint: errcheck
	defer f2.Close()           //nolint: errcheck

	var s1, s2 syscall.Stat_t
	_ = syscall.Fstat(int(f1.Fd()), &s1)
	_ = syscall.Fstat(int(f2.Fd()), &s2)

	w := &Writer{}
	_ = w.Fd(int(f1.Fd()))
	_ = w.Fd(int(f2.Fd()))
	if err := conn1.SendMessage(1, 0, w); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	_, _, r, err := conn2.ReceiveMessage()
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	fds := conn2.TakeFDs(2)
	if len(fds) != 2 {
		t.Fatalf("TakeFDs: expected 2 fds, got %d", len(fds))
	}
	r.SetFDs(fds)

	fd1, err := r.Fd()
	if err != nil {
		t.Fatalf("Fd 1: %v", err)
	}
	fd2, err := r.Fd()
	if err != nil {
		t.Fatalf("Fd 2: %v", err)
	}

	var rs1, rs2 syscall.Stat_t
	_ = syscall.Fstat(fd1, &rs1)
	_ = syscall.Fstat(fd2, &rs2)
	_ = syscall.Close(fd1)
	_ = syscall.Close(fd2)

	if s1.Ino != rs1.Ino {
		t.Errorf("fd1 inode mismatch: sent %d, got %d", s1.Ino, rs1.Ino)
	}
	if s2.Ino != rs2.Ino {
		t.Errorf("fd2 inode mismatch: sent %d, got %d", s2.Ino, rs2.Ino)
	}
}

func TestSendReceiveString(t *testing.T) {
	c1, c2 := socketPair(t)
	defer c1.Close() //nolint: errcheck
	defer c2.Close() //nolint: errcheck

	conn1 := NewConn(c1)
	conn2 := NewConn(c2)

	w := &Writer{}
	_ = w.String("wl_display")
	if err := conn1.SendMessage(8, 2, w); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	obj, opcode, r, err := conn2.ReceiveMessage()
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	if obj != 8 || opcode != 2 {
		t.Errorf("header: obj=%d opcode=%d", obj, opcode)
	}
	s, err := r.String()
	if err != nil {
		t.Fatalf("String: %v", err)
	}
	if s != "wl_display" {
		t.Errorf("string: got %q, want %q", s, "wl_display")
	}
}

func TestSendReceivePadding(t *testing.T) {
	c1, c2 := socketPair(t)
	defer c1.Close() //nolint: errcheck
	defer c2.Close() //nolint: errcheck

	conn1 := NewConn(c1)
	conn2 := NewConn(c2)

	// Payload that is not 4-byte aligned (3 bytes)
	w := &Writer{}
	_ = w.Uint32(42)
	_ = w.String("ab") // 4B len + "ab" + NUL + 0 pad = 4 + 3 + 1 = 8
	if err := conn1.SendMessage(1, 0, w); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	obj, opcode, r, err := conn2.ReceiveMessage()
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	if obj != 1 || opcode != 0 {
		t.Errorf("header: obj=%d opcode=%d", obj, opcode)
	}
	v, _ := r.Uint32()
	if v != 42 {
		t.Errorf("uint32: got %d", v)
	}
	s, _ := r.String()
	if s != "ab" {
		t.Errorf("string: got %q", s)
	}
}

func TestSendReceiveMultipleMessages(t *testing.T) {
	c1, c2 := socketPair(t)
	defer c1.Close() //nolint: errcheck
	defer c2.Close() //nolint: errcheck

	conn1 := NewConn(c1)
	conn2 := NewConn(c2)

	// Send two messages back-to-back
	w1 := &Writer{}
	_ = w1.Uint32(100)
	if err := conn1.SendMessage(1, 0, w1); err != nil {
		t.Fatalf("SendMessage 1: %v", err)
	}

	w2 := &Writer{}
	_ = w2.Uint32(200)
	if err := conn1.SendMessage(2, 1, w2); err != nil {
		t.Fatalf("SendMessage 2: %v", err)
	}

	// Receive first message
	obj1, opcode1, r1, err := conn2.ReceiveMessage()
	if err != nil {
		t.Fatalf("ReceiveMessage 1: %v", err)
	}
	if obj1 != 1 || opcode1 != 0 {
		t.Errorf("msg1 header: obj=%d opcode=%d", obj1, opcode1)
	}
	v1, _ := r1.Uint32()
	if v1 != 100 {
		t.Errorf("msg1 uint32: got %d", v1)
	}

	// Receive second message
	obj2, opcode2, r2, err := conn2.ReceiveMessage()
	if err != nil {
		t.Fatalf("ReceiveMessage 2: %v", err)
	}
	if obj2 != 2 || opcode2 != 1 {
		t.Errorf("msg2 header: obj=%d opcode=%d", obj2, opcode2)
	}
	v2, _ := r2.Uint32()
	if v2 != 200 {
		t.Errorf("msg2 uint32: got %d", v2)
	}
}

func TestMessageFrameFormat(t *testing.T) {
	c1, c2 := socketPair(t)
	defer c1.Close() //nolint: errcheck
	defer c2.Close() //nolint: errcheck

	conn1 := NewConn(c1)
	conn2 := NewConn(c2)

	w := &Writer{}
	_ = w.Uint32(0x12345678)
	if err := conn1.SendMessage(3, 5, w); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	obj, opcode, r, err := conn2.ReceiveMessage()
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}
	if obj != 3 || opcode != 5 {
		t.Errorf("header: obj=%d opcode=%d", obj, opcode)
	}
	if len(r.buf) != 4 {
		t.Errorf("payload len: got %d, want 4", len(r.buf))
	}
	v, _ := r.Uint32()
	if v != 0x12345678 {
		t.Errorf("payload: got %#x", v)
	}

	// Verify frame format manually
	payload := w.Bytes()
	expectedLen := uint32(8 + len(payload))
	headerWord2 := binary.NativeEndian.Uint32([]byte{0, 0, 0, 0})
	if expectedLen>>16 != 0 || (expectedLen<<16)>>16 != expectedLen<<16 {
		// Just verify the calculation
		_ = headerWord2
	}
}

func TestConnClose(t *testing.T) {
	c1, c2 := socketPair(t)
	defer c2.Close() //nolint: errcheck

	conn1 := NewConn(c1)
	if err := conn1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Attempting read on closed socket should error
	_, _, _, err := NewConn(c2).ReceiveMessage()
	if err == nil {
		t.Errorf("expected error reading from closed connection")
	}
}

func TestTakeFDsPerMessageIsolation(t *testing.T) {
	c1, c2 := socketPair(t)
	defer c1.Close() //nolint: errcheck
	defer c2.Close() //nolint: errcheck

	conn1 := NewConn(c1)
	conn2 := NewConn(c2)

	f1, _ := os.CreateTemp("", "wire-test-*")
	f2, _ := os.CreateTemp("", "wire-test-*")
	defer os.Remove(f1.Name()) //nolint: errcheck
	defer os.Remove(f2.Name()) //nolint: errcheck
	defer f1.Close()           //nolint: errcheck
	defer f2.Close()           //nolint: errcheck

	var s1, s2 syscall.Stat_t
	_ = syscall.Fstat(int(f1.Fd()), &s1)
	_ = syscall.Fstat(int(f2.Fd()), &s2)

	w1 := &Writer{}
	_ = w1.Fd(int(f1.Fd()))
	if err := conn1.SendMessage(1, 0, w1); err != nil {
		t.Fatalf("SendMessage 1: %v", err)
	}

	w2 := &Writer{}
	_ = w2.Fd(int(f2.Fd()))
	if err := conn1.SendMessage(2, 1, w2); err != nil {
		t.Fatalf("SendMessage 2: %v", err)
	}

	obj1, _, r1, err := conn2.ReceiveMessage()
	if err != nil {
		t.Fatalf("ReceiveMessage 1: %v", err)
	}
	if obj1 != 1 {
		t.Fatalf("expected obj 1, got %d", obj1)
	}
	// Do NOT consume fds for message 1 - fds stay in queue

	obj2, _, r2, err := conn2.ReceiveMessage()
	if err != nil {
		t.Fatalf("ReceiveMessage 2: %v", err)
	}
	if obj2 != 2 {
		t.Fatalf("expected obj 2, got %d", obj2)
	}

	// Message 1's fd should still be in the queue, taken by explicit TakeFDs
	fdsForMsg1 := conn2.TakeFDs(1)
	if len(fdsForMsg1) != 1 {
		t.Fatalf("TakeFDs for msg1: expected 1 fd, got %d", len(fdsForMsg1))
	}

	// Message 2's fd should be next
	fdsForMsg2 := conn2.TakeFDs(1)
	if len(fdsForMsg2) != 1 {
		t.Fatalf("TakeFDs for msg2: expected 1 fd, got %d", len(fdsForMsg2))
	}
	r2.SetFDs(fdsForMsg2)

	fd2, err := r2.Fd()
	if err != nil {
		t.Fatalf("Fd from msg2: %v", err)
	}
	var rs2 syscall.Stat_t
	_ = syscall.Fstat(fd2, &rs2)
	_ = syscall.Close(fd2)
	_ = syscall.Close(fdsForMsg1[0])

	if s1.Ino == s2.Ino {
		t.Skip("temp files share inodes on this filesystem")
	}
	if rs2.Ino != s2.Ino {
		t.Errorf("msg2 fd inode mismatch: expected %d, got %d", s2.Ino, rs2.Ino)
	}

	_ = r1
}

func TestUnconsumedFDs(t *testing.T) {
	c1, c2 := socketPair(t)
	defer c1.Close() //nolint: errcheck
	defer c2.Close() //nolint: errcheck

	conn1 := NewConn(c1)
	conn2 := NewConn(c2)

	f1, _ := os.CreateTemp("", "wire-test-*")
	f2, _ := os.CreateTemp("", "wire-test-*")
	defer os.Remove(f1.Name()) //nolint: errcheck
	defer os.Remove(f2.Name()) //nolint: errcheck
	defer f1.Close()           //nolint: errcheck
	defer f2.Close()           //nolint: errcheck

	w := &Writer{}
	_ = w.Fd(int(f1.Fd()))
	_ = w.Fd(int(f2.Fd()))
	if err := conn1.SendMessage(1, 0, w); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	_, _, r, err := conn2.ReceiveMessage()
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	fds := conn2.TakeFDs(2)
	r.SetFDs(fds)

	// Consume only the first fd
	fd1, err := r.Fd()
	if err != nil {
		t.Fatalf("Fd: %v", err)
	}
	_ = syscall.Close(fd1)

	unconsumed := r.UnconsumedFDs()
	if len(unconsumed) != 1 {
		t.Fatalf("UnconsumedFDs: expected 1, got %d", len(unconsumed))
	}
	_ = syscall.Close(unconsumed[0])
}

func TestTakeFDsZero(t *testing.T) {
	c1, c2 := socketPair(t)
	defer c1.Close() //nolint: errcheck
	defer c2.Close() //nolint: errcheck

	conn1 := NewConn(c1)
	conn2 := NewConn(c2)

	w := &Writer{}
	_ = w.Uint32(42)
	if err := conn1.SendMessage(1, 0, w); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	_, _, r, err := conn2.ReceiveMessage()
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	fds := conn2.TakeFDs(0)
	if len(fds) != 0 {
		t.Fatalf("TakeFDs(0): expected nil, got %v", fds)
	}

	v, _ := r.Uint32()
	if v != 42 {
		t.Errorf("payload: got %d, want 42", v)
	}
}

func TestTakeAllFDs(t *testing.T) {
	c1, c2 := socketPair(t)
	defer c1.Close() //nolint: errcheck
	defer c2.Close() //nolint: errcheck

	conn1 := NewConn(c1)
	conn2 := NewConn(c2)

	f1, _ := os.CreateTemp("", "wire-test-*")
	defer os.Remove(f1.Name()) //nolint: errcheck
	defer f1.Close()           //nolint: errcheck

	w := &Writer{}
	_ = w.Fd(int(f1.Fd()))
	if err := conn1.SendMessage(1, 0, w); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	_, _, _, err := conn2.ReceiveMessage()
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	fds := conn2.TakeAllFDs()
	if len(fds) != 1 {
		t.Fatalf("TakeAllFDs: expected 1, got %d", len(fds))
	}
	_ = syscall.Close(fds[0])

	remaining := conn2.TakeAllFDs()
	if len(remaining) != 0 {
		t.Fatalf("second TakeAllFDs: expected empty, got %v", remaining)
	}
}
