# reconnect

A client that keeps reconnecting when its server goes away. It waits a little longer after each failure (exponential backoff) and randomizes each wait (jitter). Database drivers, message-queue clients and gRPC all do this.

Only the client carries the idea, so `nc` plays the server.

## Run

```sh
go run ./network/reconnect      # terminal 1: start it first; nothing is listening yet
nc -l 127.0.0.1 9001            # terminal 2: the "server"; type lines to send them
                                # Ctrl-C crashes it; run it again to restart it
```

Two outages, from a real run:

```
connection refused: retry 1 in 71ms
connection refused: retry 2 in 161ms
connection refused: retry 3 in 375ms
connection refused: retry 4 in 410ms
connection refused: retry 5 in 972ms
connection refused: retry 6 in 2.188s
connection refused: retry 7 in 2.894s
connected to 127.0.0.1:9001
got: "hello"
got: "world"
disconnected from 127.0.0.1:9001: EOF     ← server crashed
connection refused: retry 1 in 80ms       ← short waits again
connection refused: retry 2 in 180ms
connection refused: retry 3 in 317ms
connection refused: retry 4 in 488ms
connection refused: retry 5 in 1.442s
connection refused: retry 6 in 3.083s
connected to 127.0.0.1:9001
got: "back"
```

## The loop

```go
delay := baseDelay // 100ms
attempt := 0       // only for the log; the waits don't depend on it
for {
	conn, err := net.Dial("tcp", "127.0.0.1:9001")
	if err != nil {
		attempt++
		wait := delay/2 + rand.N(delay/2)  // jitter: somewhere in [delay/2, delay)
		log.Printf("%v: retry %d in %v", err, attempt, wait.Round(time.Millisecond))
		time.Sleep(wait)
		delay = min(delay*2, maxDelay)     // backoff: 100ms, 200ms, 400ms ... 5s, 5s
		continue
	}
	delay = baseDelay // connected: the next outage starts short again
	attempt = 0
	log.Printf("connected to %s", conn.RemoteAddr())
	err = handle(conn) // reads lines until the connection ends
	log.Printf("disconnected from %s: %v", conn.RemoteAddr(), err)
	// back to the top: dial a NEW connection
}
```

## Retry the connection, not the Write

When a connection fails, throw it away and dial a new one. Retrying a `Write` on the same connection is the tempting alternative, and it's wrong for three reasons:

```go
n, err := conn.Write(buf)
// 1. err != nil can still mean n > 0: part of buf was already sent.
//    Retrying all of buf sends those n bytes twice and corrupts the stream.
// 2. Most write errors are permanent. "broken pipe" and "connection reset by peer"
//    mean the connection is dead; waiting won't bring it back.
// 3. TCP already retransmits lost packets in the kernel. By the time your
//    program sees an error, the retrying has already happened.
```

**Don't use `net.Error`'s `Temporary()`.** It used to be the way to ask "is this worth retrying?", and it's now deprecated. `go doc net.Error`: *"Temporary errors are not well-defined. Most 'temporary' errors are timeouts, and the few exceptions are surprising. Do not use this method."* To detect a timeout from a deadline you set, use `errors.Is(err, os.ErrDeadlineExceeded)`.

**Resend a request only if doing it twice is safe.** Real clients retry a whole request on a new connection, but only when it's idempotent. Fetching data twice is fine; charging a card twice is not. This wheel only reads, so it doesn't have to decide.

## Exponential backoff

A server that's down won't come back in 10ms. Retrying at a fixed fast rate hammers it, and multiply that by every client and the restarting server gets buried. Doubling the wait backs off quickly.

**Cap the wait.** Without a cap, a 10-minute outage would grow into hour-long waits. With a 5s cap, the client checks at least every 5s.

**Reset after a success.** A new outage is a new event, so it starts from 100ms again. You can see this in both runs above.

**The cap is a tradeoff.** In the first run, the server came back during the 2.894s wait, and the client noticed only when the wait ended. A bigger cap means less load during long outages but a slower recovery.

## Jitter

Imagine 1000 clients connected to a server that restarts. Without jitter, they all fail at the same moment and compute the same waits, so they retry at the same instant, every round, and knock the server over each time it comes back. That's the **thundering herd**. A random part in each wait spreads them out.

There are two common formulas, named in the AWS Architecture Blog post *Exponential Backoff And Jitter*:

```go
wait := delay/2 + rand.N(delay/2) // equal jitter (this wheel): somewhere in [delay/2, delay)
wait := rand.N(delay)             // full jitter: somewhere in [0, delay)
```

That post found that full jitter spreads clients out best and does the least total work. Equal jitter guarantees a minimum wait, so a retry never fires almost immediately.

## Two bugs worth knowing

**Doubling the jittered wait instead of the delay:**

```go
delay = min(wait*2, maxDelay)  // wrong: wait is in [delay/2, delay), so this grows by
                               // a random factor between 1 and 2, not by 2
delay = min(delay*2, maxDelay) // right: jitter changes each sleep, not the schedule
```

Low random draws pile up, so the backoff barely grows. A simulation of 8 retries:

```
double wait    50ms  60ms  90ms 140ms 160ms 260ms 450ms 590ms   ← still hammering
double delay   90ms 110ms 330ms 660ms 1.57s 2.88s 3.32s 3.91s
```

**Computing the delay from the attempt count with a shift:**

```go
d := min(baseDelay<<attempt, maxDelay) // looks fine, overflows in a long outage
```

`time.Duration` is an int64 of nanoseconds, and the shift wraps around:

```
100ms<<6  = 6.4s
100ms<<36 = 1908874h…        still positive; min() clamps it to 5s
100ms<<37 = -1306346h…       negative: min() picks it
100ms<<64 = 0s
```

With a negative or zero delay, `rand.N` panics, a few minutes into an outage. Capping the shift (`min(attempt, 6)`) works, but the 6 is a magic number tied to both constants. This wheel tracks `delay` itself instead. It's doubled with `min` and never goes over 5s, so it can't overflow, and `attempt` is just a number for the log. Libraries that compute from the attempt count multiply in a loop that stops at the cap instead of shifting.

## In practice

**Libraries** wrap this in one of two shapes: a type that hands out the next wait (`cenkalti/backoff`'s `NextBackOff`/`Reset`, Kubernetes' `wait.Backoff.Step`) or a function of the attempt count (gRPC's `backoff.Strategy`, AWS SDK retryers). Application code usually writes the plain loop, as here.

**Servers back off too, in the Accept loop.** When `Accept` fails with something like "too many open files", `net/http`'s server sleeps (5ms, doubling up to 1s) instead of spinning. Like this wheel, it tracks the delay itself (`tempDelay *= 2`, capped), not an attempt count. It's also the one place a "temporary error" check still lives in the standard library.

## Notes

**`handle` returns why the connection ended.** It returns `io.EOF` when the server closed cleanly (`Scan` stops and `Err()` is nil), and the read error otherwise. The loop logs the reason and dials again either way.

**Not here yet:** giving up after a number of attempts or on Ctrl-C (a context), and resending an unfinished request after reconnecting.
