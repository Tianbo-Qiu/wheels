package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"time"
)

const pingInterval = 5 * time.Second
const idleTime = 15 * time.Second

func main() {
	conn, err := net.Dial("tcp", "127.0.0.1:9001")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	go heartbeat(conn, pingInterval)

	scanner := bufio.NewScanner(conn)
	for {
		conn.SetReadDeadline(time.Now().Add(idleTime))

		if !scanner.Scan() {
			err := scanner.Err()
			if err == nil {
				err = io.EOF
			}
			log.Printf("server lost: %v", err)
			return
		}

		log.Print(scanner.Text())
	}
}

func heartbeat(conn net.Conn, interval time.Duration) {
	for range time.Tick(interval) {
		if _, err := fmt.Fprintln(conn, "PING"); err != nil {
			log.Print(err)
			return
		}
	}
}
