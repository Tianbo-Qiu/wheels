# tcpecho

A TCP server that writes back every byte it receives, plus a client that sends lines you type. It's the smallest pair of programs that shows how a TCP server and client are shaped.

## Run

```sh
go run ./network/tcpecho/server     # terminal 1
go run ./network/tcpecho/client     # terminal 2: type lines, Ctrl-D to quit
nc 127.0.0.1 9000                   # also works as a client
```

## Server ideas

**Listening socket vs connected socket.** `net.Listen` creates one socket whose only job is to receive new connections. Each `Accept` returns a *new* socket for one client. One listener, many connections.

**The kernel does the handshake.** By the time `Accept` returns, the SYN, SYN-ACK, ACK exchange is already done. Clients waiting to be accepted sit in a queue in the kernel.

**Server vs handler.** `main` accepts connections and hands each one off. `handle` owns one connection and holds all the application logic. The accept loop stays the same from wheel to wheel; only the handler changes.

**One goroutine per connection.** `Read` blocks, so each client gets its own goroutine. Blocking code stays simple, and the Go runtime uses kqueue/epoll underneath.

**A connection is a byte stream, not messages.** `Read` returns whatever bytes have arrived so far. Echo exactly `buf[:n]`, never the whole buffer.

**Errors are handled at their scope.** If `Listen` fails, the server can't run, so it exits. If `Accept` fails, it skips that attempt and keeps going. If a read or write fails, only that connection closes.

**Log only what the error doesn't already say.** `net` errors already include the operation and addresses, so log them as they are. `io.EOF` prints just `EOF`, so add the client address.

## Client ideas

**`Dial` is the other half of `Accept`.** The server opens passively (it listens and waits). The client opens actively: `Dial` sends the SYN and returns once the handshake is done. If nothing is listening, you get `connection refused`.

**Both sides get the same `net.Conn`.** Once connected, client and server are symmetric. Both just `Read` and `Write` bytes. "Client" only means "the side that dialed".

**The client has a port too.** The kernel picks an ephemeral port, shown by `LocalAddr()`. It's the same address the server logs as the remote address.

**The newline is a delimiter.** `Scanner.Text()` strips `\n`, and the client adds it back. Without it, the stream would be `hellohi` and nobody could tell where one message ends. That's framing in its simplest form.

**`bufio.Scanner` turns a stream into tokens.** `Scan` keeps reading until its split function (lines, by default) finds a complete token, and keeps leftover bytes for next time. `Scan` returns `false` both when input ends and when reading fails, so always check `Err()` after the loop.

## Closing

**EOF means "the peer finished sending", nothing more.** It can still be reading (a half-close). In this server everything has been echoed by the time EOF arrives, so closing loses nothing.

**Ctrl-C on the client also gives the server EOF.** The process dies without running `defer conn.Close()`, but the kernel closes its sockets and sends a FIN anyway. The same happens with `kill -9`. The server can't tell a polite exit from a killed process. If a protocol needs to know, it must say so in its own messages (e.g. a `QUIT` command).

**When it isn't EOF.** If a process dies while it still has unread data, the kernel sends an RST and the peer sees `connection reset by peer`. If a machine loses power or its network disappears, nothing is sent at all, and the other side's `Read` blocks forever.

## Experiments

1. **Watch the packets.** Run `sudo tcpdump -i lo0 port 9000`, then connect, send a line and quit. You'll see the handshake, the data and the FINs.
2. **Match the ports.** The client logs `connected … from 127.0.0.1:XXXXX`; find the same port in the server's `accepted connection from` line.
3. **Quit three ways.** Ctrl-D, Ctrl-C and `kill -9` on the client all show up as `EOF` in the server log.
4. **Remove `go`** from `go handle(conn)` and connect two clients. The second is accepted by the kernel but never served until the first leaves.
5. **Stop the server** and run the client: `connection refused`.
6. **Overflow the Scanner.** `head -c 70000 /dev/zero | tr '\0' a | go run ./network/tcpecho/client` fails with `token too long`, which is why the `Err()` check exists.

## Not here yet (on purpose)

These get added when a later wheel actually needs them:

- **Message boundaries.** The client assumes one `Read` returns the whole reply. TCP doesn't promise that. That's the next wheel: framing.
- **Deadlines.** A peer that vanishes silently holds a goroutine forever. That becomes a problem once dead peers become a problem.
- **Graceful shutdown.** Ctrl-C kills the server and the OS closes every socket. That's fine while the server holds no state.
- **Backoff on Accept errors.** A persistent error, such as hitting the file descriptor limit, spins the loop. You can see it with `ulimit -n 20` and lots of `nc` connections.
