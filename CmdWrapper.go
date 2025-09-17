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
	// this channel used by the cmd wrapper
	inputChan chan StdinManagerMsg
	// this channel used only to signal that an attach is requested
	// use for when cmd wrapper not executing
	attachChan chan bool
}

func StartStdinManager() (*StdinManager, error) {
	stdinMgr := &StdinManager{
		inputChan:  make(chan StdinManagerMsg),
		attachChan: make(chan bool),
		attached:   false,
		rb:         NewRingBuf(16),
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
		sm.attached = true
		endOfSigil := loc[1]
		dataAfterSigil := bufferBytes[endOfSigil:]

		sm.attachChan <- true // this has to be before the terminal modeset and inputChan
		sm.inputChan <- StdinManagerMsg{
			attach: ATTACHING,
			data:   string(dataAfterSigil),
		}
		if ts, err := term.MakeRaw(stdinFd()); err == nil {
			sm.termState = ts
		} else {
			log.Printf("failed to set terminal to raw mode: %v", err)
			return n, err
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
	sm.attached = false
	sm.inputChan <- StdinManagerMsg{attach: DETACHING}
	sm.attachChan <- false
}

func CmdWrapper(args []string, closeChan chan bool, stdinMgr *StdinManager) (func() error, error) {
	cmd := exec.Command(args[0], args[1:]...)
	ptx, err := pty.Start(cmd)
	if err != nil {
		log.Fatal("couldn't run command ", err)
	}
	log.Printf("Executing %s (PID %d)", cmd.String(), cmd.Process.Pid)
	startTime := time.Now()
	outRb := NewRingBuf(4096)

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
		time.AfterFunc(5*time.Second, func() {
			if cmd.ProcessState == nil {
				cmd.Process.Kill()
				log.Println("no response to sigint, sent sigkill")
			}
		})
		return err
	}, nil
}

func stdinFd() int {
	return int(os.Stdin.Fd())
}
