package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/gliderlabs/ssh"
	"golang.org/x/crypto/bcrypt"
)

var PWD_ENVVAR = "SSH_RESTARTER_PWD"

func main() {
	sshPort := flag.Uint("ssh_port", 22, "port for the ssh server to listen on")
	fwdPort := flag.Uint("fwd_port", 25565, "destination port on the server for all forwards")
	sshHostKeyPath := flag.String("hostkey", "/etc/ssh/ssh_host_rsa", "path to the ssh host key")
	sshPwdPtr := flag.String("pwd", "", fmt.Sprintf("the bcrypt of the server password. if not provided, will read from %s envvar", PWD_ENVVAR))
	idleTimoutSec := flag.Uint("timeout", 600, "seconds with 0 connections before terminating process")
	maxRetries := flag.Uint("retries", 20, "max number of 5s retries for a client to connect to the destination port")
	flag.Parse()

	if len(flag.Args()) == 0 {
		log.Fatal("you must provide a command to run")
	}
	sshPwd := *sshPwdPtr
	if sshPwd == "" {
		sshPwd = os.Getenv(PWD_ENVVAR)
		if sshPwd == "" {
			log.Fatal("SSH password provided neither as CLI arg or env var")
		}
	}
	sshPwdBytes := []byte(sshPwd)

	loginChangeChan := make(chan LoginChange)

	server := ssh.Server{
		Addr: fmt.Sprintf(":%d", *sshPort),
		LocalPortForwardingCallback: ssh.LocalPortForwardingCallback(func(ctx ssh.Context, dhost string, dport uint32) bool {
			ok := (dhost == "localhost" || dhost == "127.0.0.1") && dport == uint32(*fwdPort)
			if ok {
				return true
			}
			log.Println("rejected port request to", dhost, dport)
			return false
		}),
		Handler: ssh.Handler(func(s ssh.Session) {
			io.WriteString(s, "Port forwarding only!\r\n")
			io.WriteString(s, fmt.Sprintf(
				"Try this command: ssh -L %d:localhost:%d -N <server address> -p %d\r\n",
				*fwdPort, *fwdPort, *sshPort))
			s.Exit(1)
		}),
		ChannelHandlers: map[string]ssh.ChannelHandler{
			"direct-tcpip": MakeRestarterTCPHandler(loginChangeChan, *maxRetries),
			"session":      ssh.DefaultSessionHandler,
		},
	}
	ssh.HostKeyFile(*sshHostKeyPath)(&server)
	ssh.PasswordAuth(func(ctx ssh.Context, pass string) bool {
		err := bcrypt.CompareHashAndPassword(sshPwdBytes, []byte(pass))
		if err != nil {
			log.Println("could not bcrypt", err)
		}
		return err == nil
	})(&server)
	log.Println("Starting ssh server on port", *sshPort, "accepting forwards to", *fwdPort)
	go Restarter(flag.Args(), loginChangeChan, *idleTimoutSec)
	log.Fatal(server.ListenAndServe(), nil)
}
