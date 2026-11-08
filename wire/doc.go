// Package wire implements the Wayland wire format and the SCM_RIGHTS
// descriptor side channel.
//
//	[object_id:uint32][length:uint16<<16 | opcode:uint16][payload...]
//
// length covers the 8-byte header and the payload and is a multiple of 4.
// Integers use the host byte order, as specified by the protocol.
//
// Descriptors are not tagged with a message: the kernel reports no boundary
// between an SCM_RIGHTS control message and the bytes it arrived with. Conn
// queues every received descriptor and the dispatcher assigns them by the fd
// count the interface declares for the event opcode. That is why an unknown
// opcode is fatal: its count is unknown, so a guess mis-assigns every later
// descriptor.
//
// Wire never duplicates a descriptor or closes one it has handed out. Callers
// of Writer.Fd and of TakeFDs own what they get; the dispatcher closes what no
// handler took; Conn.Close drains the rest.
//
// SendMessage is safe for concurrent use; ReceiveMessage is single-goroutine.
package wire
