# tlv

A server that reads type-length-value messages, and a client that writes them. Each message says how long it is, so the reader knows exactly where it ends, whatever bytes the value contains.

## Run

```sh
go run ./network/tlv/server     # terminal 1
go run ./network/tlv/client     # terminal 2: sends three messages
```

```
client: sent type 1, 5 bytes        server: text 5 bytes: "hello"
client: sent type 2, 4 bytes        server: binary 4 bytes: ab cd ef 12
client: sent type 1, 5 bytes        server: text 5 bytes: "world"
                                    server: 127.0.0.1:63714: done
```

## The format

TLV stands for **Type-Length-Value**. Every message is a 5-byte header followed by the value:

```
type    length (big-endian uint32)    value
01      00 00 00 05                   68 65 6c 6c 6f     text "hello"
02      00 00 00 04                   ab cd ef 12        binary
```

**The length tells the reader where the message ends.** [scanwords](../scanwords) split on spaces, so a value could never contain a space. With a length prefix, the value can be any bytes at all, binary included. Nothing needs escaping, and the reader doesn't scan for anything.

**The type says what the value means.** The server prints text with `%q` and binary as hex. Both sides must agree on the numbers, because nothing in the bytes says what type 1 is.

## Reading one message

```go
func readMsg(r io.Reader) (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err // 0 bytes then EOF: io.EOF; 1-4 bytes then EOF: io.ErrUnexpectedEOF
	}

	typ := header[0]
	if typ != typeText && typ != typeBinary {
		return 0, nil, fmt.Errorf("unknown type %d", typ)
	}

	n := binary.BigEndian.Uint32(header[1:]) // 00 00 00 05 → 5
	if n > maxLen { // 4 bytes from someone else: check before allocating
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
```

**`io.ReadFull` reads exactly n bytes.** [readloop](../readloop) showed that one `Read` returns however much has arrived. `ReadFull` calls `Read` in a loop until the buffer is full.

**Never trust the length.** It comes from the other side. Without `maxLen`, a header of `ff ff ff ff` makes the server allocate 4 GiB for one message.

**On any error, the connection is dropped.** After a bad length or an unknown type, the server no longer knows where the next message starts, so it can't keep reading.

## Writing one message

```go
func writeMsg(w io.Writer, typ byte, val []byte) error {
	var header [5]byte
	header[0] = typ
	binary.BigEndian.PutUint32(header[1:], uint32(len(val))) // 5 → 00 00 00 05
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(val)
	return err
}
```

`writeMsg` is `readMsg` in reverse. Two `Write`s are fine: the server finds the message's end from the length, not from how the bytes are grouped when they arrive.

## Every case

The Go client only sends well-formed messages. To send broken ones, write the bytes yourself with `printf` and pipe them into `nc`. macOS `nc` half-closes when its input ends, so the server sees EOF right after the last byte.

```sh
printf '\x01\x00\x00'                                | nc 127.0.0.1 9001   # cut header
printf '\x01\x00\x00\x00\x05hel'                     | nc 127.0.0.1 9001   # cut value
printf '\x01\x00\x00\x00\x05'                        | nc 127.0.0.1 9001   # header, no value
printf '\x01\xff\xff\xff\xffhi'                      | nc 127.0.0.1 9001   # claims 4 GiB
printf '\x07\x00\x00\x00\x01x'                       | nc 127.0.0.1 9001   # unknown type
printf '\x01\x00\x00\x00\x02hi\x02\x00\x00\x00\x03abc' | nc 127.0.0.1 9001 # two in one write
```

The server logs, in the same order:

```
127.0.0.1:63715: unexpected EOF
127.0.0.1:63716: unexpected EOF
127.0.0.1:63717: unexpected EOF                     ← without the fix: "done"
127.0.0.1:63718: length 4294967295 over limit 10485760
127.0.0.1:63719: unknown type 7
text 2 bytes: "hi"
binary 3 bytes: 61 62 63
127.0.0.1:63720: done
```

The last case shows the framing at work. Two messages arrived in one piece, and the lengths split them apart.

## io.EOF vs io.ErrUnexpectedEOF

`io.EOF` means **the input ended where an end is allowed**. `io.ErrUnexpectedEOF` means **it ended in the middle of something**, so the data was cut off.

```go
buf := make([]byte, 5)

// peer sent nothing, then closed
io.ReadFull(conn, buf) // 0, io.EOF               ← nothing started: a clean end

// peer sent "he", then closed
io.ReadFull(conn, buf) // 2, io.ErrUnexpectedEOF  ← started, couldn't finish

// peer sent "hello"
io.ReadFull(conn, buf) // 5, nil
```

A plain `Read` only ever returns `io.EOF`, because it doesn't know how much you expected. `ReadFull` knows (`len(buf)`), so it can tell the two apart.

For `ReadFull`, "unexpected" is relative to one call. When the value read gets zero bytes, `ReadFull` returns plain `io.EOF`, because from its point of view nothing started. But the header came first, so the message is cut. That's why `readMsg` converts it. `binary.Read`, `gzip` and `json.Decoder` follow the same convention: an end mid-structure is `io.ErrUnexpectedEOF`.

## ReadFull blocks

`ReadFull` returns only when the buffer is full, the peer closes, or a read fails:

```go
io.ReadFull(conn, buf) // buf is 5 bytes

// "hello" arrives (in one piece or several)  → 5, nil
// "he" arrives, then the peer closes          → 2, io.ErrUnexpectedEOF
// "he" arrives, then the peer goes quiet      → blocks, waiting for "llo"
```

A sender that promises 5 bytes and then stalls holds the handler goroutine until it closes:

```sh
(printf '\x01\x00\x00\x00\x05he'; sleep 60) | nc 127.0.0.1 9001
# the server logs nothing for 60s, then "unexpected EOF" when nc exits
```

A real server sets a read deadline before each `readMsg`, as in [heartbeat](../heartbeat). The deadline applies to every `Read` inside `ReadFull`, so a message that stalls halfway also times out. This wheel leaves it out to stay about framing.

## Big-endian

Both sides just need to **agree** on the byte order. Big-endian ("network byte order") is the convention for network protocols:

```go
binary.BigEndian.PutUint32(b, 5)    // 00 00 00 05   most significant byte first
binary.LittleEndian.PutUint32(b, 5) // 05 00 00 00   least significant byte first

binary.LittleEndian.Uint32([]byte{0x00, 0x00, 0x00, 0x05}) // 83886080, not 5
```

- **Convention:** IP, TCP and UDP headers are big-endian, and most protocols follow them.
- **Readable:** in a hex dump, `00 00 01 00` is 256, in the order you'd write it.
- **Independent of the CPU:** your Mac is little-endian. Copying an integer's memory onto the wire would send `05 00 00 00`. `binary.BigEndian` produces the same bytes on every machine.

## Go details

**Write out wire values; don't use `iota`.** The numbers are part of the protocol. With `iota`, reordering or inserting a constant silently changes what type 1 means on one side.

**Without `iota`, a missing value repeats the line above. It doesn't count up:**

```go
const (
	typeText = 1
	typeBinary   // also 1, not 2
)
```

This bug happened while building the client. Every binary message went out as type 1, and the server printed it as text.

**`typ`, not `type`.** `type` is a keyword. The standard library uses `typ` for a local variable. An exported struct field can be `Type`, because a capitalized name isn't the keyword.

**Don't name a variable `len`.** It shadows the builtin, so any later `len(x)` in that function fails to compile.

## In the real world

TLV and length-prefixed framing are everywhere in binary protocols:

- **PostgreSQL's wire protocol** is almost this exact format: a 1-byte type, a 4-byte big-endian length, then the body. One difference is that its length counts its own 4 bytes.
- **HTTP/2 frames** have a 3-byte length, a 1-byte type, flags and a stream ID, then the payload.
- **TLS** handshake messages and extensions (SNI, ALPN) are type + length + value.
- **X.509 certificates** use ASN.1 DER, which is TLV nested inside TLV. There the "T" is called a tag.
- **DHCP options, RADIUS attributes, BGP path attributes:** in networking, "options" fields are almost always TLV.
- **Protocol Buffers** prefix every field with a tag, and strings or nested messages also with a length.

**Why it's popular:** because the length is known, a reader can **skip a type it doesn't understand** and keep going. That's how old clients survive new protocol versions. This server rejects unknown types instead. A real protocol would read the value, ignore it, and carry on.
