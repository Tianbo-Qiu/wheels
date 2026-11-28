package main

import (
	"bufio"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"time"
)

const (
	baseDelay = 100 * time.Millisecond
	maxDelay  = 5 * time.Second
)

func main() {
	delay := baseDelay
	attempt := 0
	for {
		conn, err := net.Dial("tcp", "127.0.0.1:9001")
		if err != nil {
			attempt++
			// delay with jitter
			wait := delay/2 + rand.N(delay/2)
			log.Printf("%v: retry %d in %v", err, attempt, wait.Round(time.Millisecond))
			time.Sleep(wait)
			// backoff
			delay = min(delay*2, maxDelay)
			continue
		}
		delay = baseDelay
		attempt = 0
		log.Printf("connected to %s", conn.RemoteAddr())
		err = handle(conn)
		log.Printf("disconnected from %s: %v", conn.RemoteAddr(), err)
	}
}

func handle(conn net.Conn) error {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		log.Printf("got: %q", scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return io.EOF
}
