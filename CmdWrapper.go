package main

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty" // PTY: Import the library
	"golang.org/x/term"
)

type cmdWrapper struct {
	args []string
	cmd  *exec.Cmd
	// PTY: We only need one RingBuf since PTY merges stdout and stderr.
	outputRb   *RingBuf
	attachChan chan bool
	running    bool
	// PTY: We need a field to hold the master end of the PTY.
	ptmx       *os.File
	startTime  time.Time
	waitChan   chan bool
	killInited bool
	termState  *term.State
}

func NewCmdWrapper(args []string, waitChan chan bool) *cmdWrapper {
	return &cmdWrapper{
		args: args,
		// PTY: Initialize the single output buffer.
		outputRb:   NewRingBuf(2 << 10), // Increased size for combined output
		attachChan: make(chan bool),
		running:    false,
		waitChan:   waitChan,
		killInited: false,
	}
}

func (cw *cmdWrapper) Run() {
	log.Println("running:", cw.args)
	if cw.running {
		log.Println("cmd was already running, killing the cmd")
		cw.Kill()
	}

	cw.cmd = exec.Command(cw.args[0], cw.args[1:]...)
	ptmx, err := pty.Start(cw.cmd)
	if err != nil {
		log.Fatalf("failed to start pty: %v", err)
	}
	// PTY: Ensure the PTY master file is closed when the function exits.
	cw.ptmx = ptmx

	// PTY: The process is now running. Launch the I/O managers.
	go cw.stdinManager(cw.ptmx)  // Pass the PTY as the writer for stdin
	go cw.outputManager(cw.ptmx) // Pass the PTY as the reader for output
	cw.running = true
	cw.startTime = time.Now()
	cw.killInited = false
	go func() {
		defer ptmx.Close()
		cw.cmd.Wait()
		if cw.termState != nil {
			term.Restore(int(os.Stdin.Fd()), cw.termState)
			cw.termState = nil
		}
		log.Printf("Process uptime: %v", time.Since(cw.startTime))
		cw.running = false
		cw.waitChan <- cw.killInited
	}()
}

// PTY: The stdin manager now writes to the PTY file.
func (cw *cmdWrapper) stdinManager(ptmx io.Writer) {
	attached := false
	buf := make([]byte, 128)

	for {
		if !attached {
			log.Println("In detached mode. Type 'term' and press Enter to attach.")
			scanner := bufio.NewScanner(os.Stdin)
			for scanner.Scan() {
				if scanner.Text() == "term" {
					log.Println("Attaching terminal, press CTRL+A to detach.")
					ts, err := term.MakeRaw(int(os.Stdin.Fd()))
					if err != nil {
						log.Println("failed to make terminal raw:", err)
						continue
					}
					cw.termState = ts
					attached = true
					cw.attachChan <- true
					break
				}
			}
		} else { // attached == true
			for {
				n, err := os.Stdin.Read(buf)
				if err != nil { // This still handles stdin closing unexpectedly
					term.Restore(int(os.Stdin.Fd()), cw.termState)
					attached = false
					cw.attachChan <- false
					log.Println("Terminal detached due to stdin error.")
					break
				}

				// Check for CTRL+A (ASCII value 1)
				if idx := bytes.IndexByte(buf[:n], 1); idx != -1 {
					// If there was any valid input before CTRL+A, send it.
					if idx > 0 {
						ptmx.Write(buf[:idx])
					}

					// Perform the detach sequence
					term.Restore(int(os.Stdin.Fd()), cw.termState)
					attached = false
					cw.attachChan <- false // Signal to detach
					log.Println("Terminal detached via CTRL+A.")
					break // Exit the attached loop
				}

				// If no detach key was found, send the input to the process.
				if n > 0 {
					ptmx.Write(buf[:n])
				}
			}
		}
	}
}

// PTY: This function now manages the single, merged output stream.
func (cw *cmdWrapper) outputManager(ptmx io.Reader) {
	// Start a goroutine to continuously pump data from the PTY into the ring buffer.
	go io.Copy(cw.outputRb, ptmx)

	// This loop listens for signals and manages the attach/detach state.
	for {
		shouldBeAttached := <-cw.attachChan

		if shouldBeAttached {
			// PTY: Attach the single output buffer to the user's terminal.
			cw.outputRb.Attach(os.Stdout)
		} else {
			cw.outputRb.Detach()
		}
	}
}

func (cw *cmdWrapper) Kill() {
	cw.killInited = true
	if !cw.running || cw.cmd == nil || cw.cmd.Process == nil {
		log.Println("Kill requested, but no process is running.")
		return
	}

	log.Println("Attempting graceful shutdown with SIGINT...")

	// Send the interrupt signal (like Ctrl+C)
	if err := cw.cmd.Process.Signal(os.Interrupt); err != nil {
		log.Printf("Failed to send SIGINT: %v. Forcing kill.", err)
		// If sending SIGINT fails, go straight to SIGKILL
		if killErr := cw.cmd.Process.Kill(); killErr != nil {
			log.Printf("Failed to send SIGKILL: %v", killErr)
		}
		return
	}
}
