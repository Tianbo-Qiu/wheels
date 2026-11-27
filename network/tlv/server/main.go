package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
)

// type | length | value
// 1B     4B       <= 10M
const (
	typeText   = 1
	typeBinary = 2
)

const maxLen = 10 << 20 // 10 MiB

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

	for {
		typ, val, err := readMsg(conn)
		if err == io.EOF {
			log.Printf("%s: done", conn.RemoteAddr())
			return
		}
		if err != nil {
			log.Printf("%s: %v", conn.RemoteAddr(), err)
			return
		}

		switch typ {
		case typeText:
			log.Printf("text %d bytes: %q", len(val), val)
		case typeBinary:
			log.Printf("binary %d bytes: % x", len(val), val)
		}
	}
}

// Returns message type, value and error if any.
func readMsg(r io.Reader) (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}

	typ := header[0]
	if typ != typeText && typ != typeBinary {
		return 0, nil, fmt.Errorf("unknown type %d", typ)
	}

	n := binary.BigEndian.Uint32(header[1:])
	if n > maxLen {
		return 0, nil, fmt.Errorf("length %d over limit %d", n, maxLen)
	}
	value := make([]byte, n)

	if _, err := io.ReadFull(r, value); err != nil {
		// header arrived but the value didn't: ReadFull says io.EOF (0 bytes read),
		// but the message is cut, so report it as io.ErrUnexpectedEOF
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return 0, nil, err
	}

	return typ, value, nil
}
