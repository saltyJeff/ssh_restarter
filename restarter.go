package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"time"

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

func MakeRestarterTCPHandler(loginChan chan LoginChange, maxRetries uint) ssh.ChannelHandler {
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
		var err error
		var dconn net.Conn
		const retryDelay = 5 * time.Second

		for i := uint(0); i < maxRetries; i++ {
			// Attempt to dial the connection
			dconn, err = dialer.DialContext(ctx, "tcp", dest)
			if err == nil {
				// If the error is nil, the connection succeeded. Break the loop.
				break
			}
			log.Printf("Connection attempt %d/%d failed: %v. Retrying in %v...\n", i+1, maxRetries, err, retryDelay)
			if i == maxRetries-1 {
				break
			}
			time.Sleep(retryDelay)
		}
		if err != nil {
			newChan.Reject(gossh.ConnectionFailed, fmt.Sprintf("failed to connect after %d attempts: %v", maxRetries, err))
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

func Restarter(cmdline []string, loginChan chan LoginChange, idleTimoutSec uint) {
	closeChan := make(chan bool)
	sessions := map[string]bool{}
	stdinMgr, _ := StartStdinManager()
	cmdKiller, _ := CmdWrapper(cmdline, closeChan, stdinMgr)
	log.Println("first execution, program termination scheduled")
	timeoutDuration := time.Duration(idleTimoutSec) * time.Second
	timer := time.NewTimer(timeoutDuration)
	attached := false

	updateState := func() {
		log.Println("terminal attached?", attached, "# sessions", len(sessions))
		shouldStopTimer := attached || len(sessions) != 0
		if shouldStopTimer {
			timer.Stop()
			if cmdKiller == nil {
				cmdKiller, _ = CmdWrapper(cmdline, closeChan, stdinMgr)
			}
		} else {
			log.Println("no users, scheduling program termination")
			timer.Reset(timeoutDuration)
		}
	}

	for {
		select {
		case login := <-loginChan:
			if login.newLogin {
				sessions[login.loginId] = true

			} else {
				delete(sessions, login.loginId)
			}
			log.Println("number of connections:", len(sessions))
			updateState()
		case <-timer.C:
			if cmdKiller != nil {
				cmdKiller()
			}
		case attach := <-stdinMgr.attachChan:
			attached = attach
			updateState()
		case closeRequested := <-closeChan:
			howClosed := "unexpectedly"
			if closeRequested {
				howClosed = "expectedly"
			}
			log.Println("command terminated", howClosed)
			cmdKiller = nil
			updateState()
		}
	}
}
