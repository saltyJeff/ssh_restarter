package main

import (
	"io"
	"sync"
)

// RingBuf is a thread-safe, fixed-size ring buffer that implements io.Writer.
// It preserves the last N bytes of data written to it.
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

// Write saves data to the buffer, overwriting the oldest data if the buffer is full.
// It is optimized to use `copy` instead of a byte-by-byte loop.
func (rb *RingBuf) Write(p []byte) (n int, err error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.attachedWriter != nil {
		rb.attachedWriter.Write(p)
	}

	originalLen := len(p)

	// If the input is larger than the buffer, we only care about the last part.
	if len(p) > len(rb.buf) {
		p = p[len(p)-len(rb.buf):]
	}

	bytesToWrite := len(p)
	spaceToEnd := len(rb.buf) - rb.pos

	if bytesToWrite > spaceToEnd {
		// The write wraps around the end of the buffer.
		copy(rb.buf[rb.pos:], p[:spaceToEnd])
		copy(rb.buf[0:], p[spaceToEnd:])
		rb.pos = bytesToWrite - spaceToEnd
		rb.full = true
	} else {
		// The write fits without wrapping.
		copy(rb.buf[rb.pos:], p)
		rb.pos += bytesToWrite
		if rb.pos == len(rb.buf) {
			// Exactly filled the buffer to the end.
			rb.pos = 0
			rb.full = true
		}
	}

	// Per the io.Writer contract, return the length of the original slice.
	return originalLen, nil
}

// Len returns the number of bytes currently stored in the buffer.
func (rb *RingBuf) Len() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if rb.full {
		return len(rb.buf)
	}
	return rb.pos
}

// Bytes returns the contents of the buffer as a single, ordered slice of bytes.
func (rb *RingBuf) Bytes() []byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.bytes()
}

// bytes is the internal, unlocked implementation for retrieving buffer contents.
func (rb *RingBuf) bytes() []byte {
	if !rb.full {
		return rb.buf[:rb.pos]
	}
	// When full, "unroll" the buffer by combining the two segments.
	return append(rb.buf[rb.pos:], rb.buf[:rb.pos]...)
}

// DumpAttach sets an attached writer and dumps the current buffer contents to it.
func (rb *RingBuf) DumpAttach(writer io.Writer) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Call the internal, unlocked bytes() method to avoid deadlock.
	writer.Write(rb.bytes())
	rb.attachedWriter = writer
}

// Detach disconnects the io.Writer.
func (rb *RingBuf) Detach() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.attachedWriter = nil
}

// Reset clears the buffer.
func (rb *RingBuf) Reset() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.pos = 0
	rb.full = false
}
