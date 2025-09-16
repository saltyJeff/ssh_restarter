package main

import (
	"io"
	"log"
	"net"
	"strconv"

	"github.com/gliderlabs/ssh"
	gossh "golang.org/x/crypto/ssh"
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

func Restarter(cmdline []string, loginChan chan LoginChange) {
	closeChan := make(chan bool)
	sessions := map[string]bool{}
	stdinMgr, _ := StartStdinManager()
	for {
		cmd, _ := CmdWrapper(cmdline, closeChan, stdinMgr)

		select {
		case login := <-loginChan:
			if login.newLogin {
				sessions[login.loginId] = true
				if cmd == nil {
					cmd, _ = CmdWrapper(cmdline, closeChan, stdinMgr)
				}
			} else {
				delete(sessions, login.loginId)
			}
			log.Println("number of connections:", len(sessions))
		case closeRequested := <-closeChan:
			if !closeRequested {
				log.Println("command closed extraneously!")
			}
			cmd = nil
		}
	}
}
