package main

import (
	"bytes"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

const (
	ATTACHING = iota
	DETACHING = iota
	NOCHANGE  = iota
)

type StdinManagerMsg struct {
	attach int
	data   string
}
type StdinManager struct {
	termState *term.State
	attached  bool
	rb        *RingBuf
	inputChan chan StdinManagerMsg
}

func StartStdinManager() (*StdinManager, error) {
	stdinMgr := &StdinManager{
		inputChan: make(chan StdinManagerMsg),
		attached:  false,
		rb:        NewRingBuf(8),
	}
	ts, err := term.GetState(stdinFd())
	stdinMgr.termState = ts
	if err != nil {
		return nil, err
	}
	go io.Copy(stdinMgr, os.Stdin)
	return stdinMgr, nil
}

var termSigil = regexp.MustCompile(`term\r?\n`)

func (sm *StdinManager) Write(p []byte) (n int, err error) {
	if !sm.attached {
		n, err = sm.rb.Write(p)
		if err != nil {
			return n, err
		}
		bufferBytes := sm.rb.Bytes()
		loc := termSigil.FindIndex(bufferBytes)
		if loc == nil {
			return n, nil
		}
		log.Println("terminal attached. CTRL+A to detach")

		if ts, err := term.MakeRaw(stdinFd()); err == nil {
			sm.termState = ts
		} else {
			log.Printf("failed to set terminal to raw mode: %v", err)
			return n, err
		}
		sm.attached = true
		endOfSigil := loc[1]
		dataAfterSigil := bufferBytes[endOfSigil:]

		sm.inputChan <- StdinManagerMsg{
			attach: ATTACHING,
			data:   string(dataAfterSigil),
		}
		sm.rb.Reset()
	} else {
		// CTRL+A == 1
		idx := bytes.IndexByte(p, 1)
		if idx == -1 {
			sm.inputChan <- StdinManagerMsg{
				attach: NOCHANGE,
				data:   string(p),
			}
		} else {
			sm.inputChan <- StdinManagerMsg{
				attach: NOCHANGE,
				data:   string(p[:idx]),
			}
			sm.Detach()
		}
	}
	return len(p), nil
}

func (sm *StdinManager) Detach() {
	term.Restore(stdinFd(), sm.termState)
	log.Println("terminal detached. enter \"term\" to attach")
	sm.attached = false
	sm.inputChan <- StdinManagerMsg{attach: DETACHING}
}

func CmdWrapper(args []string, closeChan chan bool, stdinMgr *StdinManager) (func() error, error) {
	cmd := exec.Command(args[0], args[1:]...)
	ptx, err := pty.Start(cmd)
	log.Printf("Executing %s (PID %d)", cmd.String(), cmd.Process.Pid)
	if err != nil {
		return nil, err
	}
	startTime := time.Now()
	outRb := NewRingBuf(2048)

	// attach/detach stdin
	quitChan := make(chan struct{})
	termState, err := term.GetState(stdinFd())
	if err != nil {
		return nil, err
	}
	// pipe tty to the ringbuffer
	go io.Copy(outRb, ptx)

	// attach/detach stdin/stdout
	go func() {
		for {
			select {
			case <-quitChan:
				return
			case msg := <-stdinMgr.inputChan:
				switch msg.attach {
				case ATTACHING:
					outRb.DumpAttach(os.Stdout)
				case NOCHANGE:
					ptx.Write([]byte(msg.data))
				case DETACHING:
					outRb.Detach()
				}
			}
		}
	}()
	log.Println("enter \"term\" to attach")

	// cleanup goroutine
	closeRequested := false
	go func() {
		cmd.Wait()
		term.Restore(stdinFd(), termState)
		log.Printf("Process exited. Status code %d uptime: %v", cmd.ProcessState.ExitCode(), time.Since(startTime))
		stdinMgr.Detach()
		outRb.Detach()
		close(quitChan)
		ptx.Close()
		closeChan <- closeRequested
	}()

	return func() error {
		closeRequested = true
		err := cmd.Process.Signal(os.Interrupt)
		log.Println("sent sigint")
		return err
	}, nil
}

func stdinFd() int {
	return int(os.Stdin.Fd())
}
