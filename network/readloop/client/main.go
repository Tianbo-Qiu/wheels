package main

import (
	"io"
	"log"
	"net"
	"time"
)

const size = 512 << 10 // 512 KiB

func main() {
	conn, err := net.Dial("tcp", "127.0.0.1:9001")
	if err != nil {
		log.Fatal(err)
	}

	buffer := make([]byte, size)
	reads, total := 0, 0
	ts := time.Now()
	for {
		n, err := conn.Read(buffer)
		if n > 0 {
			reads++
			total += n
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("read %d bytes from %s", n, conn.RemoteAddr())
	}
	log.Printf("done. read %d bytes in total from %d reads in %v", total, reads, time.Since(ts).Round(time.Millisecond))
}
