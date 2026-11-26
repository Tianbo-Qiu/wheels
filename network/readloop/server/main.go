package main

import (
	"crypto/rand"
	"log"
	"net"
	"time"
)

const size = 16 << 20 // 16 MiB

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
		go send(conn)
	}
}

func send(conn net.Conn) {
	defer conn.Close()

	buffer := make([]byte, size)
	rand.Read(buffer)

	ts := time.Now()
	n, err := conn.Write(buffer)
	if err != nil {
		log.Print(err)
		return
	}

	log.Printf("wrote %d bytes to %s in one Write in %v", n, conn.RemoteAddr(), time.Since(ts).Round(time.Millisecond))
}
