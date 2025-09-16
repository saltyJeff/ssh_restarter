package main

import (
	"io"
	"sync"
)

// RingBuf is a thread-safe, fixed-size ring buffer that implements io.Writer.
type RingBuf struct {
	buf            []byte
	pos            int  // Current writing position
	full           bool // True if the buffer has been completely filled at least once
	mu             sync.Mutex
	attachedWriter io.Writer
}

// NewRingBuf creates a new RingBuf with the specified size.
func NewRingBuf(bufSize int) *RingBuf {
	if bufSize <= 0 {
		panic("bufSize must be positive")
	}
	return &RingBuf{
		buf: make([]byte, bufSize),
	}
}

// Write implements the io.Writer interface. It saves the last len(buf) bytes
// of data from p and forwards the write to an attached writer, if any.
func (rb *RingBuf) Write(p []byte) (n int, err error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// If an io.Writer is attached, forward the write to it immediately.
	if rb.attachedWriter != nil {
		// We ignore the result of this write as the primary function
		// is to fill the internal buffer.
		rb.attachedWriter.Write(p)
	}

	// Efficiently copy the incoming data into the ring buffer.
	// This handles cases where p is larger than the buffer itself.
	for _, b := range p {
		rb.buf[rb.pos] = b
		rb.pos++
		if rb.pos >= len(rb.buf) {
			rb.pos = 0
			rb.full = true
		}
	}

	return len(p), nil
}

// Attach connects an io.Writer. It first dumps the entire current buffer
// contents to the writer and then forwards all subsequent writes to it.
func (rb *RingBuf) Attach(writer io.Writer) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Dump the current contents of the buffer to the new writer.
	writer.Write(rb.bytes())

	rb.attachedWriter = writer
}

// Detach disconnects the io.Writer, stopping any future writes from being forwarded.
func (rb *RingBuf) Detach() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.attachedWriter = nil
}

// bytes returns the contents of the buffer as a single, ordered slice of bytes.
// This is an internal helper that must be called with the mutex held.
func (rb *RingBuf) bytes() []byte {
	if !rb.full {
		// If the buffer hasn't wrapped yet, the data is just the initial part.
		return rb.buf[:rb.pos]
	}
	// If the buffer has wrapped, the data is from the current position to the end,
	// followed by the data from the beginning to the current position.
	// append is an efficient way to concatenate these two slices.
	return append(rb.buf[rb.pos:], rb.buf[:rb.pos]...)
}
