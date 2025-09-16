package main

import (
	"flag"
	"fmt"
	"io"
	"log"

	"github.com/gliderlabs/ssh"
)

func main() {
	sshPort := flag.Uint("ssh_port", 22, "port for the ssh server to listen on")
	fwdPort := flag.Uint("fwd_port", 25565, "destination port on the server for all forwards")
	sshHostKeyPath := flag.String("ssh_host_key_path", "/etc/ssh/ssh_host_rsa", "path to the ssh host key")
	flag.Parse()

	loginChangeChan := make(chan LoginChange)

	server := ssh.Server{
		Addr: fmt.Sprintf(":%d", *sshPort),
		LocalPortForwardingCallback: ssh.LocalPortForwardingCallback(func(ctx ssh.Context, dhost string, dport uint32) bool {
			ok := dhost == "localhost" && dport == uint32(*fwdPort)
			if ok {
				return true
			}
			log.Println("rejected port request to", dhost, dport)
			return false
		}),
		Handler: ssh.Handler(func(s ssh.Session) {
			io.WriteString(s, "Port forwarding only!\r\n")
			io.WriteString(s, fmt.Sprintf(
				"Try this command: ssh -L %d:localhost%d -N <server address> -p %d\r\n",
				*fwdPort, *fwdPort, *sshPort))
			s.Exit(1)
		}),
		ChannelHandlers: map[string]ssh.ChannelHandler{
			"direct-tcpip": MakeRestarterTCPHandler(loginChangeChan),
			"session":      ssh.DefaultSessionHandler,
		},
	}
	ssh.HostKeyFile(*sshHostKeyPath)(&server)
	ssh.PasswordAuth(func(ctx ssh.Context, pass string) bool {
		log.Println("Password", pass)
		return true
	})(&server)
	log.Println("Starting ssh server on port", *sshPort, "accepting forwards to", *fwdPort)
	go Restarter(flag.Args(), loginChangeChan)
	log.Fatal(server.ListenAndServe(), nil)
}
