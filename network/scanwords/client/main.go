package main

import (
	"log"
	"net"
	"time"
)

const payload = "the bigger the interface, the weaker the abstraction."

func main() {
	conn, err := net.Dial("tcp", "127.0.0.1:9001")
	if err != nil {
		log.Fatal(err)
	}

	for i := 0; i < len(payload); i += 4 {
		piece := payload[i:min(i+4, len(payload))]
		n, err := conn.Write([]byte(piece))
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("wrote %d bytes: %q", n, piece)
		time.Sleep(500 * time.Millisecond)
	}
}
