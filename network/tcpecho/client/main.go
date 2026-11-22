package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
)

func main() {
	conn, err := net.Dial("tcp", "127.0.0.1:9000")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	log.Printf("connected to %s from %s", conn.RemoteAddr(), conn.LocalAddr())

	scanner := bufio.NewScanner(os.Stdin)
	buf := make([]byte, 1024)

	for scanner.Scan() {
		if _, err := conn.Write([]byte(scanner.Text() + "\n")); err != nil {
			log.Fatal(err)
		}
		n, err := conn.Read(buf)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(string(buf[:n]))
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
}
