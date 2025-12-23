package main

import (
	"fmt"
	"github.com/giwealth/gssh"
)

func main() {
	ssh := &gssh.Server{
		Options: gssh.ServerOptions{
			Addr: "192.168.1.173",
			Port: "22",
			User: "root",
			KeyFile: "/root/.ssh/id_rsa",
		},
	}

	stdout, err := ssh.Command("sudo ls -l /data")
	if err != nil {
		panic(err)
	}
	fmt.Println(stdout)
}
