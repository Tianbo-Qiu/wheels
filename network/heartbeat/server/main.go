package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"time"
)

const idleTime = 15 * time.Second

func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:9001")
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("listening on %s", ln.Addr())

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Print(err)
			continue
		}
		go handle(conn)
	}
}

func handle(conn net.Conn) {
	defer conn.Close()

	log.Printf("%s connected", conn.RemoteAddr())

	scanner := bufio.NewScanner(conn)
	for {
		conn.SetReadDeadline(time.Now().Add(idleTime))

		if !scanner.Scan() {
			err := scanner.Err()
			if err == nil {
				err = io.EOF
			}
			log.Printf("closing %s: %v", conn.RemoteAddr(), err)
			return
		}

		reply := scanner.Text()
		if reply == "PING" {
			reply = "PONG"
		}

		if _, err := fmt.Fprintln(conn, reply); err != nil {
			log.Print(err)
			return
		}
	}
}
