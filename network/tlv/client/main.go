package main

import (
	"encoding/binary"
	"io"
	"log"
	"net"
)

const (
	typeText   = 1
	typeBinary = 2
)

func main() {
	conn, err := net.Dial("tcp", "127.0.0.1:9001")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	msgs := []struct {
		typ byte
		val []byte
	}{
		{typeText, []byte("hello")},
		{typeBinary, []byte{0xab, 0xcd, 0xef, 0x12}},
		{typeText, []byte("world")},
	}

	for _, msg := range msgs {
		if err := writeMsg(conn, msg.typ, msg.val); err != nil {
			log.Fatal(err)
		}
		log.Printf("sent type %d, %d bytes", msg.typ, len(msg.val))
	}
}

// type | length | value
// 1B     4B       <= 10M
func writeMsg(w io.Writer, typ byte, val []byte) error {
	var header [5]byte
	header[0] = typ
	binary.BigEndian.PutUint32(header[1:], uint32(len(val)))
	// two writes is fine
	// write header
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	// write value
	_, err := w.Write(val)
	return err
}
