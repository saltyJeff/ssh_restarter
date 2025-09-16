package main

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"

	"github.com/gliderlabs/ssh"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

type LoginChange struct {
	newLogin bool
	loginId  string
}
type localForwardChannelData struct {
	DestAddr string
	DestPort uint32

	OriginAddr string
	OriginPort uint32
}

func MakeRestarterTCPHandler(loginChan chan LoginChange) ssh.ChannelHandler {
	return func(srv *ssh.Server, conn *gossh.ServerConn, newChan gossh.NewChannel, ctx ssh.Context) {
		d := localForwardChannelData{}
		if err := gossh.Unmarshal(newChan.ExtraData(), &d); err != nil {
			newChan.Reject(gossh.ConnectionFailed, "error parsing forward data: "+err.Error())
			return
		}

		if srv.LocalPortForwardingCallback == nil || !srv.LocalPortForwardingCallback(ctx, d.DestAddr, d.DestPort) {
			newChan.Reject(gossh.Prohibited, "port forwarding is disabled")
			return
		}

		dest := net.JoinHostPort(d.DestAddr, strconv.FormatInt(int64(d.DestPort), 10))

		loginId := ctx.RemoteAddr().String()
		loginChan <- LoginChange{newLogin: true, loginId: loginId}

		var dialer net.Dialer
		dconn, err := dialer.DialContext(ctx, "tcp", dest)
		if err != nil {
			newChan.Reject(gossh.ConnectionFailed, err.Error())
			loginChan <- LoginChange{newLogin: false, loginId: loginId}
			return
		}

		ch, reqs, err := newChan.Accept()
		if err != nil {
			dconn.Close()
			loginChan <- LoginChange{newLogin: false, loginId: loginId}
			return
		}
		go gossh.DiscardRequests(reqs)

		go func() {
			defer ch.Close()
			defer dconn.Close()
			io.Copy(ch, dconn)
			loginChan <- LoginChange{newLogin: false, loginId: loginId}
		}()
		go func() {
			defer ch.Close()
			defer dconn.Close()
			io.Copy(dconn, ch)
			loginChan <- LoginChange{newLogin: false, loginId: loginId}
		}()
	}
}

func startCmd(cmdline []string) *exec.Cmd {
	cmd := exec.Command(cmdline[0], cmdline[1:]...)
	log.Println("started command:", cmd.String())
	go cmd.Run()
	return cmd
}

// manages attaching/detactching from a process
// if detatched, the goroutine listens for "term" and attaches
// if attached, the user's stdin is connected to fwdStdin until CTRL+D is entered
func stdinManager(attachChan chan bool, fwdStdin io.WriteCloser) {
	var attached = false
	var termState *term.State = nil
	buf := make([]byte, 1024)
	for {
		if !attached {
			scanner := bufio.NewScanner(os.Stdin)
			// keep scanning until we see "term"
			for scanner.Scan() {
				if scanner.Text() == "term" {
					log.Println("attaching terminal, CTRL+D to disconnect")
					ts, err := term.MakeRaw(int(os.Stdin.Fd()))
					termState = ts
					if err != nil {
						log.Println("failed to make terminal raw")
						continue
					}
					attached = true
					attachChan <- attached
					break
				}
			}
		} else {
			for {
				n, err := os.Stdin.Read(buf)
				idx := bytes.IndexByte(buf[:n], 4) // 4 corresponds to EOD
				shouldDetach := idx >= 0
				lastInput := n
				if shouldDetach {
					lastInput = idx
				}
				if err == nil {
					fwdStdin.Write(buf[:lastInput])
				}
				if err != nil || shouldDetach {
					term.Restore(int(os.Stdin.Fd()), termState)
					attached = false
					attachChan <- attached
					break
				}
			}
		}
	}
}

func Restarter(cmdline []string, loginChan chan LoginChange) {
	waitChan := make(chan bool)
	sessions := map[string]bool{}
	cmd := NewCmdWrapper(cmdline, waitChan)
	cmd.Run()
	for {
		select {
		case login := <-loginChan:
			if login.newLogin {
				sessions[login.loginId] = true
				if !cmd.running {
					cmd.Run()
				}
			} else {
				delete(sessions, login.loginId)
			}
			log.Println("number of connections:", len(sessions))
		case requestedKill := <-waitChan:
			if !requestedKill {
				log.Println("command closed extraneously!")
			}
			cmd.Run()
		}
	}
}
