package main

import (
	"github.com/260by/gssh"
)

func main() {
	ssh := &gssh.Server{
		Options: gssh.ServerOptions{
			Addr: "192.168.1.173",
			Port: "22",
			User: "root",
			KeyFile: "/home/keith/public_key/local",
		},
	}

	// upload
	err := ssh.Put("tmp/test-logs/request*", "/tmp/ttt")
	if err != nil {
		panic(err)
	}

	// download
	err = ssh.Get("/data/test-logs/", "tmp")
	if err != nil {
		panic(err)
	}
}
