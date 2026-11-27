package main

import (
	"bufio"
	"log"
	"net"
)

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

	scanner := bufio.NewScanner(conn)
	scanner.Split(bufio.ScanWords)

	words := 0
	for scanner.Scan() {
		words++
		log.Printf("got word: %q", scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		log.Printf("%s: %v", conn.RemoteAddr(), err)
		return
	}

	log.Printf("done, got %d words from %s", words, conn.RemoteAddr())
}
