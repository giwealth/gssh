package main

import (
	"fmt"
	"github.com/260by/gssh"
)

func main() {
	ssh := &gssh.Server{
		Options: gssh.ServerOptions{
			Addr: "10.111.1.12",
			Port: "22",
			User: "root",
			KeyFile: "/root/.ssh/internal",
		},
		ProxyOptions: gssh.ServerOptions{
			Addr: "12.43.34.9",
			Port: "22",
			User: "root",
			KeyFile: "/root/.ssh/id_rsa",
		},
	}

	stdout, err := ssh.Command("ls -l /data/logs")
	if err != nil {
		panic(err)
	}
	fmt.Println(stdout)

	err = ssh.Get("/root", "tmp")
	if err != nil {
		panic(err)
	}

	err = ssh.Put("tmp/a.txt", "/root")
	if err != nil {
		panic(err)
	}
}
