package main

import (
	"log"
	"net"
)

func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:9000")
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("server listening on %s", ln.Addr())

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println(err)
			continue
		}

		go handle(conn)
	}
}

func handle(conn net.Conn) {
	defer func() {
		log.Printf("closing connection to %s", conn.RemoteAddr())
		conn.Close()
	}()

	log.Printf("accepted connection from %s", conn.RemoteAddr())

	buf := make([]byte, 1024)

	for {
		n, err := conn.Read(buf)
		if err != nil {
			log.Printf("read from %s: %v", conn.RemoteAddr(), err)
			return
		}
		log.Printf("read %d bytes from %s", n, conn.RemoteAddr())

		n, err = conn.Write(buf[:n])
		if err != nil {
			log.Println(err)
			return
		}
		log.Printf("wrote %d bytes to %s", n, conn.RemoteAddr())
	}
}
