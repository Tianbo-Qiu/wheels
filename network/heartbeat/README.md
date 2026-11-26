# heartbeat

A server and client that each notice when the other side stops responding. The client sends `PING` every 5s, and the server replies `PONG`. Each side gives up after 15s with nothing received.

## Run

```sh
go run ./network/heartbeat/server     # terminal 1
go run ./network/heartbeat/client     # terminal 2: logs PONG every 5s
nc 127.0.0.1 9001                     # a client that never pings
```

## The problem

TCP tells you when the other side *closes*: its kernel sends a FIN, and you read EOF. It doesn't tell you when the other side is *stuck*.

When a process hangs, its kernel still holds the connection open and still ACKs every packet. At the TCP level, the connection looks perfectly healthy. A `Read` waiting on that process blocks forever.

TCP keepalive (which Go turns on by default) doesn't help. It checks that the other *kernel* is reachable, and that kernel is fine.

## The idea

**A read deadline turns "waiting forever" into an error.** Set a deadline before every read. If nothing arrives in time, the read fails with `i/o timeout`.

```go
for {
	conn.SetReadDeadline(time.Now().Add(idleTime)) // now + 15s, before every read
	if !scanner.Scan() {
		// EOF          → the other side closed
		// i/o timeout  → nothing arrived for 15s
		return
	}
	// a line arrived; the loop sets a new deadline 15s from now
}
```

The deadline counts from the *last line received*, not from when the connection opened:

```
3s   "hello" arrives   → deadline = 3s + 15s  = 18s
12s  "world" arrives   → deadline = 12s + 15s = 27s
```

**PING keeps a quiet but healthy connection alive.** A client might have nothing to say for a while. A `PING` every 5s means a live peer always beats the 15s deadline, with room to miss two in a row.

**Both sides need a deadline.** The server's deadline catches a client that went quiet. The client's deadline catches a server that stopped answering. Each side trusts only what it actually receives.

**PING is just a line.** `PING` and `PONG` travel as ordinary lines in the same stream. The server echoes any other line unchanged, and any line moves its deadline.

## Every case, as a timeline

Times are counted from when the client connects. The client pings every 5s, and both sides give up after 15s of silence.

### 1. A healthy, idle client

```
0s   connect
5s   client → PING    server deadline → 20s, replies PONG    client deadline → 20s
10s  client → PING    server deadline → 25s, replies PONG    client deadline → 25s
...  forever
```

Neither deadline is ever reached. The connection stays open with no real data flowing.

### 2. A client sending data (`nc`, typing)

```
0s   connect          server deadline → 15s
3s   "hello"          server deadline → 18s, echoes "hello"
12s  "world"          server deadline → 27s, echoes "world"
27s  (no more lines)  server: "closing …: i/o timeout"
```

Any line counts, not only `PING`. Real traffic is already proof the client is alive.

### 3. A client that never says anything (`nc`, idle)

```
0s   connect                    server deadline → 15s
15s  server: Scan fails         "closing …: i/o timeout"
     server closes              its kernel sends FIN
     nc keeps running           it doesn't notice the FIN until it writes
     you type a line in nc      the server's kernel answers with RST
     nc exits
```

A silent client is dropped after 15s. `nc` only finds out when it tries to send.

### 4. The client quits (Ctrl-C, Ctrl-D, even `kill -9`)

```
7s   client process exits      its kernel closes the socket, sends FIN
7s   server: Scan returns false, Err() == nil → "closing …: EOF"
```

This is detected immediately. When a process dies, its kernel closes all of its sockets, so a FIN always goes out.

### 5. The client freezes (`kill -STOP`)

```
5s   last PING                  server deadline → 20s
7s   client frozen              its kernel keeps the socket open: no FIN
10s  (no PING)
20s  server: "closing …: i/o timeout"
     server closes              FIN to the frozen client's kernel
     kill -CONT on the client   its deadline (20s) has already passed
     client: "server lost: … i/o timeout"
```

This is the case the heartbeat exists for. TCP said the connection was fine the whole time, but the program had stopped responding.

After it resumes, the client reports a timeout, not EOF. Its own deadline expired while it was frozen, and an expired deadline fails the read before the FIN is even looked at.

### 6. The server freezes (`kill -STOP`)

```
5s   last PONG                  client deadline → 20s
7s   server frozen
10s  client → PING              server's kernel ACKs and buffers it; no PONG
15s  client → PING              same
20s  client: "server lost: … i/o timeout"
     client closes              FIN to the frozen server's kernel
     kill -CONT on the server   its deadline (20s) has already passed
     server: "closing …: i/o timeout"
     server closes              the PINGs are still unread → its kernel sends RST
```

The pings were *delivered and acknowledged* at the TCP level. Only the missing PONG shows that the server stopped responding.

The RST at the end is harmless: the client is already gone, so nobody sees it.

### 7. The server quits (Ctrl-C)

```
9s   server process exits       its kernel sends FIN
9s   client: Scan returns false → "server lost: EOF"
```

This is immediate, for the same reason as case 4.

### 8. The machine or network disappears

Examples are power loss or an unplugged cable. You can't reproduce this on localhost.

```
     the other machine is gone  no FIN, no RST, nothing at all
     ...silence...
     deadline reached           "i/o timeout"
```

To the surviving side, this looks exactly like case 5 or 6. The deadline catches it the same way.

### The pattern

If the *process* exits, its kernel sends a FIN, and the other side knows right away (cases 4 and 7).

If something goes silent *without its kernel closing the socket*, only the deadline finds out (cases 3, 5, 6 and 8).

The heartbeat keeps a healthy idle connection from looking like a silent one (case 1).

## Closing: FIN and RST

**FIN means "I won't send any more."** Your kernel sends it when you call `Close()` or when your process exits. The other side's `Read` returns EOF.

**RST means "this connection doesn't exist here."** A kernel sends it in two situations:
- data arrives for a socket that is fully closed, or
- a socket is closed while it still has unread data (the end of case 6).

### Full close

`conn.Close()` stops both directions. Usually the other side closes too:

```
you  ──── data ───→  peer
you  ──── FIN ────→  peer     you called Close(); peer reads EOF
you  ←─── FIN ────   peer     peer calls Close() too; the connection is gone
```

If the peer writes after you closed, your kernel rejects the data:

```
you  ──── FIN ────→  peer     you called Close(); peer reads EOF
you  ←─── data ────  peer     peer writes anyway; its Write succeeds (only buffered)
you  ──── RST ────→  peer     your kernel sends this, not your program
                              peer's next Write → "broken pipe"
                              peer's next Read  → "connection reset by peer"
```

This is case 3 with `nc`. The server closed at 15s, `nc` wrote a line, and the RST came back.

### Half close

`conn.(*net.TCPConn).CloseWrite()` stops only *your* sending direction. You can still read:

```
you  ──── data ───→  peer
you  ──── FIN ────→  peer     peer reads EOF: no more data from you
you  ←─── data ────  peer     peer keeps sending, you keep reading, no RST
you  ←─── FIN ────   peer     peer is done too; now the connection is fully closed
```

This is useful for saying "that's all my input, now send the answer" (`nc -N` does it when its input ends). This wheel doesn't need it.

## The errors you'll see

**`EOF`** (`Scan` returns false with a nil `Err()`): the other side sent a FIN. It closed cleanly, or its process exited.

**`i/o timeout`**: your own read deadline passed with nothing received. The other side is frozen, silent, or unreachable. You can check for it with `errors.Is(err, os.ErrDeadlineExceeded)`.

**`broken pipe`**: you wrote after the other side sent an RST.

**`connection reset by peer`**: you read after the other side sent an RST.

The first write after the other side closes usually *succeeds*, because it only lands in your local buffer. The RST comes back as a reply to it, so the failure shows up on the **next** call. Writing three times to a connection the server already dropped looks like this:

```
write: <nil>                                   ← only buffered; this triggers the RST
write: write tcp …: write: broken pipe
write: write tcp …: write: broken pipe
read:  read tcp …: read: connection reset by peer
```

## Try it

```sh
kill -STOP <server pid>   # case 6: client logs "server lost: … i/o timeout" 15s after the last PONG
kill -CONT <server pid>   # server wakes up and logs "closing …: i/o timeout"

kill -STOP <client pid>   # case 5: server logs "closing …: i/o timeout" 15s after the last PING
kill -CONT <client pid>   # client wakes up and logs "server lost: … i/o timeout"

nc 127.0.0.1 9001         # case 3: wait 15s, then type a line; nc exits
```

## In the real world

WebSocket ping/pong frames, HTTP/2 `PING` (used by gRPC keepalive), Redis `PING`, IRC `PING`/`PONG`, MQTT `PINGREQ`, AMQP heartbeats, and SSH `ServerAliveInterval` all use this same pattern. They send something small on a timer, treat any traffic as proof of life, and give up after a few missed intervals.

## Notes

**`time.Tick` doesn't leak.** Since Go 1.23, a ticker that nothing references is garbage collected without calling `Stop`. Once `heartbeat` returns, its ticker is freed.

**Reading and writing at the same time is safe.** The `heartbeat` goroutine writes while `main` reads. `net.Conn` is safe to use from multiple goroutines.
