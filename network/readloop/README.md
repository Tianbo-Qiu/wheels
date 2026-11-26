# readloop

The server sends 16 MiB in a single `Write`. The client reads it into a 512 KiB buffer and counts how many `Read` calls it took. One `Write` on one side turns into many `Read`s of different sizes on the other.

## Run

```sh
go run ./network/readloop/server     # terminal 1
go run ./network/readloop/client     # terminal 2: one line per Read, then a total
```

A run on localhost:

```
server: wrote 16777216 bytes to 127.0.0.1:54090 in one Write in 4ms

client: read 48996 bytes from 127.0.0.1:9001
client: read 65328 bytes from 127.0.0.1:9001
client: read 32664 bytes from 127.0.0.1:9001
...
client: done. read 16777216 bytes in total from 168 reads in 13ms
```

Run it again and the count changes: 168 reads one time, 219 another. The chunk sizes change too.

## One Write, many Reads

**TCP is a byte stream, not a sequence of messages.** The receiver never sees "the 16 MiB the server wrote". It sees bytes arriving in its kernel buffer, and each `Read` takes whatever is there at that moment.

**What one `Read` returns depends on timing.** It depends on how far the sender has gotten, how full the receive buffer is, and when the client's goroutine gets to run. That's why the sizes differ on every run, and why not one `Read` filled the 512 KiB buffer.

**So you read in a loop until EOF.** The server closes the connection after its `Write`, its kernel sends a FIN, and the client's next `Read` returns `io.EOF`. That's how the client knows everything has arrived.

## The read loop

```go
for {
	n, err := conn.Read(buffer)
	if n > 0 { // use the data first: a Read can return bytes AND an error
		reads++
		total += n
	}
	if err == io.EOF { // server closed: everything has arrived
		break
	}
	if err != nil { // reset, timeout, ...: this connection is done
		log.Fatal(err)
	}
	log.Printf("read %d bytes from %s", n, conn.RemoteAddr())
}
```

**Check `n` before `err`.** The `io.Reader` contract says callers should process the `n > 0` bytes before looking at the error. A loop that breaks as soon as `err != nil` can silently drop the last bytes.

**Every error needs a way out.** A dead connection returns `n == 0` and an error on *every* call. If the loop only checks for EOF, a reset connection spins forever.

**Only `buffer[:n]` is new data.** The rest of the buffer holds whatever was there before. This client only counts bytes, but anything that uses the data must slice it to `n`.

## Write is different

**`Write` sends everything or returns an error.** In Go, `conn.Write` keeps going until all 16 MiB are handed to the kernel, so there's no write loop. Short reads are normal; short writes are not.

**`Write` returning doesn't mean the peer has the data.** The server's `Write` finished in 4ms, but the client needed 13ms to read everything. `Write` returns once the bytes are in the server's send buffer. Delivery and reading happen afterwards.

## Why it's this fast

16 MiB in about 10ms is roughly 2 GB/s, because nothing leaves the machine:

- **Loopback has no network card and no wire.** The kernel copies bytes from one socket buffer to the other, so the speed is close to memory-copy speed.
- **The round trip takes microseconds.** TCP waits for ACKs before it sends more, and on loopback the ACKs come back almost instantly.
- **Packets are big and never lost.** The macOS loopback MTU is 16384 bytes, compared with 1500 on Ethernet, and nothing is ever dropped or retransmitted.

On a real 1 Gbit/s link, 16 MiB takes at least about 134ms, and more once round trips and TCP slow start are added. The reads would be smaller and more numerous.

## The shortcut

Once the loop makes sense, the standard library runs it for you:

```go
total, err := io.Copy(io.Discard, conn) // reads until EOF; err is nil on a clean EOF
```

`io.Copy` uses the same loop: it reads chunks, handles `n` before `err`, and treats EOF as success.

## 127.0.0.1 vs localhost

`127.0.0.1` is an address. `localhost` is a name, and on macOS it resolves to two addresses (from `/etc/hosts`):

```
127.0.0.1   localhost      ← IPv4 loopback
::1         localhost      ← IPv6 loopback
```

They're the same machine but different sockets. A listener only accepts connections to the address it's bound to:

```
listen [::1]:9104       bound to [::1]:9104
  dial 127.0.0.1        refused
  dial ::1              ok
listen localhost:9105   bound to 127.0.0.1:9105    ← Go picked one of the two
  dial 127.0.0.1        ok
  dial ::1              refused
listen :9106            bound to [::]:9106         ← no host: every address
  dial 127.0.0.1        ok
  dial ::1              ok
```

- **Listen on an IP, not a name.** `go doc net.Listen` warns that a host name creates "a listener for at most one of the host's IP addresses".
- **Dialing `localhost` usually works.** Dial tries each address in turn, so a refused `::1` falls back to `127.0.0.1`.
- **Leaving out the host (`:9106`) listens on every address**, including your real network interfaces, so other machines can connect. That's why these wheels bind to `127.0.0.1`.
