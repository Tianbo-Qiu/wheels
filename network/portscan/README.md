# portscan

A TCP connect scanner: tries to connect to every port in a range, on one host or a whole subnet, and reports which ones accept.

```sh
go run ./network/portscan 127.0.0.1 1 1024
go run ./network/portscan -budget 10s 192.0.2.0/24 1 1024
```

Open ports go to stdout; errors and the summary go to stderr.

## How it got here

Each version fixes one thing the previous version got wrong. Every version below is a complete program.

### 1. Sequential dial

```go
package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 4 {
		log.Fatal("usage: portscan HOST FIRST_PORT LAST_PORT")
	}
	host := os.Args[1]
	first, err := strconv.Atoi(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	last, err := strconv.Atoi(os.Args[3])
	if err != nil {
		log.Fatal(err)
	}

	for port := first; port <= last; port++ {
		addr := net.JoinHostPort(host, strconv.Itoa(port)) // adds [] for IPv6
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			log.Print(err) // closed port: "connection refused", instantly
			continue
		}
		conn.Close() // handshake completed: the port is open
		fmt.Println(port, "open")
	}
}
```

A port is open if the handshake completes. A closed port answers instantly with an RST.

**Broke:** a host that drops packets (`192.0.2.1`) never answers. The kernel keeps retrying the SYN for ~75s per port before giving up.

### 2. Timeout per dial

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"time"
)

func main() {
	if len(os.Args) != 4 {
		log.Fatal("usage: portscan HOST FIRST_PORT LAST_PORT")
	}
	host := os.Args[1]
	first, err := strconv.Atoi(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	last, err := strconv.Atoi(os.Args[3])
	if err != nil {
		log.Fatal(err)
	}

	for port := first; port <= last; port++ {
		addr := net.JoinHostPort(host, strconv.Itoa(port))

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr) // gives up when ctx ends
		cancel()                                     // not defer: in a loop, defer waits until main returns

		if err != nil {
			log.Print(err) // silent port: "i/o timeout" after 500ms
			continue
		}
		conn.Close()
		fmt.Println(port, "open")
	}
}
```

You decide how long to wait, not the kernel.

**Broke:** still one port at a time. 100 silent ports × 500ms = 50s.

### 3. Concurrent dials

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

func main() {
	if len(os.Args) != 4 {
		log.Fatal("usage: portscan HOST FIRST_PORT LAST_PORT")
	}
	host := os.Args[1]
	first, err := strconv.Atoi(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	last, err := strconv.Atoi(os.Args[3])
	if err != nil {
		log.Fatal(err)
	}

	var wg sync.WaitGroup
	for port := first; port <= last; port++ {
		wg.Go(func() { // each iteration has its own port (Go 1.22+)
			addr := net.JoinHostPort(host, strconv.Itoa(port))

			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel() // fine now: runs when this func returns

			var d net.Dialer
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				log.Print(err)
				return
			}
			conn.Close()
			fmt.Println(port, "open") // completion order, not port order
		})
	}
	wg.Wait() // without it, main returns and kills every dial
}
```

All silent ports wait out their 500ms at the same time, so 100 ports take ~0.5s.

### 4. Whole subnets

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"time"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: portscan HOST|CIDR MIN_PORT MAX_PORT")
		os.Exit(2)
	}
	prefix, err := parsePrefix(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	minPort, err := strconv.Atoi(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	maxPort, err := strconv.Atoi(os.Args[3])
	if err != nil {
		log.Fatal(err)
	}
	if minPort < 1 || maxPort > 65535 || minPort > maxPort {
		log.Fatal("ports must be 1-65535 and MIN_PORT <= MAX_PORT") // uint16 would wrap silently
	}

	var wg sync.WaitGroup
	// Masked: "192.0.2.77/24" starts at 192.0.2.0, not .77
	// host = host.Next(): Addr is a value, Next returns a new one
	for host := prefix.Masked().Addr(); prefix.Contains(host); host = host.Next() {
		for port := minPort; port <= maxPort; port++ {
			wg.Go(func() {
				addr := netip.AddrPortFrom(host, uint16(port))

				ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
				defer cancel()

				var dialer net.Dialer
				conn, err := dialer.DialContext(ctx, "tcp", addr.String())
				if err != nil {
					log.Print(err)
					return
				}
				conn.Close()
				fmt.Println(addr, "open") // full address: many hosts now
			})
		}
	}
	wg.Wait()
}

// parsePrefix accepts one address ("10.0.0.1") or a range ("10.0.0.0/24").
// A single address becomes a /32 (or /128), a range of one.
func parsePrefix(s string) (netip.Prefix, error) {
	if addr, err := netip.ParseAddr(s); err == nil {
		return netip.PrefixFrom(addr, addr.BitLen()), nil
	}
	return netip.ParsePrefix(s)
}
```

**Broke:** a /24 × 1024 ports is 262,144 sockets at once. That exceeds a typical file limit, can overflow router and firewall connection tables (so open ports get dropped and look silent), and looks like an attack.

### 5. Cap concurrency

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"time"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: portscan HOST|CIDR MIN_PORT MAX_PORT")
		os.Exit(2)
	}
	prefix, err := parsePrefix(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	minPort, err := strconv.Atoi(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	maxPort, err := strconv.Atoi(os.Args[3])
	if err != nil {
		log.Fatal(err)
	}
	if minPort < 1 || maxPort > 65535 || minPort > maxPort {
		log.Fatal("ports must be 1-65535 and MIN_PORT <= MAX_PORT")
	}

	const maxInFlight = 1000
	sem := make(chan struct{}, maxInFlight) // 1000 slots

	var wg sync.WaitGroup
	for host := prefix.Masked().Addr(); prefix.Contains(host); host = host.Next() {
		for port := minPort; port <= maxPort; port++ {
			sem <- struct{}{} // take a slot in the loop: blocks while all 1000 are busy
			wg.Go(func() {
				defer func() { <-sem }() // give the slot back
				addr := netip.AddrPortFrom(host, uint16(port))

				ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
				defer cancel()

				var dialer net.Dialer
				conn, err := dialer.DialContext(ctx, "tcp", addr.String())
				if err != nil {
					log.Print(err)
					return
				}
				conn.Close()
				fmt.Println(addr, "open")
			})
		}
	}
	wg.Wait()
}

// parsePrefix accepts one address ("10.0.0.1") or a range ("10.0.0.0/24").
func parsePrefix(s string) (netip.Prefix, error) {
	if addr, err := netip.ParseAddr(s); err == nil {
		return netip.PrefixFrom(addr, addr.BitLen()), nil
	}
	return netip.ParsePrefix(s)
}
```

The slot is taken before `wg.Go`, so there are never more than 1000 goroutines, not 262,144 waiting ones.

**Broke:** the /24 scan now takes ~2 minutes. Ctrl-C kills it instantly, and with nothing open you can't tell "nothing found" from "barely started".

### 6. Ctrl-C stops cleanly and reports

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: portscan HOST|CIDR MIN_PORT MAX_PORT")
		os.Exit(2)
	}
	prefix, err := parsePrefix(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	minPort, err := strconv.Atoi(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	maxPort, err := strconv.Atoi(os.Args[3])
	if err != nil {
		log.Fatal(err)
	}
	if minPort < 1 || maxPort > 65535 || minPort > maxPort {
		log.Fatal("ports must be 1-65535 and MIN_PORT <= MAX_PORT")
	}

	// Ctrl-C cancels ctx instead of killing the process.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	const maxInFlight = 1000
	sem := make(chan struct{}, maxInFlight)

	var checked, open atomic.Int64 // written by many goroutines
	start := time.Now()

	var wg sync.WaitGroup
scan:
	for host := prefix.Masked().Addr(); prefix.Contains(host); host = host.Next() {
		for port := minPort; port <= maxPort; port++ {
			select {
			case sem <- struct{}{}: // got a slot
			case <-ctx.Done(): // Ctrl-C: stop starting new dials
				break scan // plain break would only leave the select
			}
			wg.Go(func() {
				defer func() { <-sem }()
				addr := netip.AddrPortFrom(host, uint16(port))

				// Child of ctx, not Background: one Ctrl-C cancels every in-flight dial.
				dialCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
				defer cancel()

				var dialer net.Dialer
				conn, err := dialer.DialContext(dialCtx, "tcp", addr.String())
				if errors.Is(err, context.Canceled) {
					return // interrupted before an answer: don't count it
				}
				checked.Add(1)
				if err != nil {
					log.Print(err)
					return
				}
				conn.Close()
				open.Add(1)
				fmt.Println(addr, "open")
			})
		}
	}
	wg.Wait() // quick after Ctrl-C: every dial was cancelled

	status := "done"
	if ctx.Err() != nil {
		status = "interrupted"
	}
	log.Printf("%s: checked %d ports, %d open, in %v",
		status, checked.Load(), open.Load(), time.Since(start).Round(time.Millisecond))
}

// parsePrefix accepts one address ("10.0.0.1") or a range ("10.0.0.0/24").
func parsePrefix(s string) (netip.Prefix, error) {
	if addr, err := netip.ParseAddr(s); err == nil {
		return netip.PrefixFrom(addr, addr.BitLen()), nil
	}
	return netip.ParsePrefix(s)
}
```

**Broke:** no way to say "scan as much as you can in 10 seconds".

### 7. Total time budget

This is the current [`main.go`](main.go). What changed from version 6:

```go
// os.Args → flag, so -budget can come before the positional arguments
budget := flag.Duration("budget", 0, "stop the whole scan after this long, e.g. 10s (0 = no limit)")
flag.Parse()
prefix, err := parsePrefix(flag.Arg(0)) // flag.Arg(0) is the first non-flag argument

// ctx now ends on Ctrl-C or when the budget runs out
if *budget > 0 {
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, *budget) // = not :=, see rule 4
	defer cancel()
}
```

The budget exposed a bug in version 6's filter:

```go
conn, err := dialer.DialContext(dialCtx, "tcp", addr.String())
if err != nil && ctx.Err() != nil {
	// The scan was stopped, so this failure says nothing about the port.
	// Ctrl-C gives "canceled"; the budget gives "i/o timeout", the same
	// error as a silent port. errors.Is(err, context.Canceled) missed it.
	return
}
```

And the summary reports why it stopped:

```go
switch ctx.Err() {
case context.Canceled:
	status = "interrupted" // Ctrl-C
case context.DeadlineExceeded:
	status = "timeout" // budget
}
```

## Mental model

**A context is a stop signal with a reason, passed down a tree.** It carries three things:

```go
<-ctx.Done()    // a channel that closes when ctx ends; never sends a value
ctx.Err()       // why it ended: nil (still running), Canceled, DeadlineExceeded
ctx.Deadline()  // when it will end on its own, if ever
```

**It ends in exactly two ways:**

```go
ctx, cancel := context.WithCancel(parent)     // someone calls cancel()  → Canceled
ctx, cancel := context.WithTimeout(parent, d) // the clock runs out      → DeadlineExceeded
                                              // ...or the parent ends   → parent's reason
```

`signal.NotifyContext` is just `WithCancel` where the signal handler calls `cancel` for you.

**A context doesn't stop anything by itself.** It only announces "stop". Code has to listen:

```go
d.DialContext(dialCtx, ...) // library calls listen for you: pass ctx in, they abort when it ends

select {                    // your own code listens with select on Done()
case sem <- struct{}{}:     // the work
case <-ctx.Done():          // or the stop signal, whichever comes first
}
```

**The other pieces each do one job:**

```go
ctx             // when to stop     (Ctrl-C, budget, per-dial timeout)
sem             // how many at once (a buffered channel used as a counter)
wg              // wait for all     (wg.Wait returns when every wg.Go func has returned)
atomic.Int64    // shared counters  (safe to update from many goroutines)
```

**One run, start to finish:**

```
loop starts dials ── each takes a slot (sem), runs under dialCtx (child of ctx)
        │
Ctrl-C or budget ── ctx ends ── every dialCtx ends with it
        │
loop's select sees ctx.Done() ── break scan: no new dials
in-flight dials return early ── skipped, because ctx.Err() != nil
        │
wg.Wait() returns ── all goroutines finished, counters are final
        │
summary ── ctx.Err() says why: nil = done, Canceled = Ctrl-C, DeadlineExceeded = budget
```

## Context rules

These are the rules every bug in this wheel came from.

**1. Contexts form a tree; cancellation flows down only.** Cancelling a parent ends every child. Cancelling a child never touches its parent.

```
signal ctx         Ctrl-C
└── budget ctx     Ctrl-C or budget         ← ctx
    └── dialCtx    any of the above or 500ms
```

**2. A child's deadline is the earlier of its own and its parent's.** A dial started 100ms before the budget ends gets 100ms, not 500ms, and fails with the same `i/o timeout` as a silent port.

**3. To ask why something stopped, ask the parent.** The dial's error can't tell "port silent" from "scan over". `ctx.Err()` on the scan's context can: `nil` while running, `Canceled` after Ctrl-C, `DeadlineExceeded` after the budget.

**4. Wrapping a context conditionally needs `=`, not `:=`.** Inside an `if`, `ctx, cancel := ...` declares a new `ctx` that disappears at the `}`. It compiles and silently does nothing.

**5. Never reuse the name `ctx` in an inner scope.** `ctx, cancel := context.WithTimeout(ctx, ...)` inside the goroutine hides the scan's `ctx`, so `ctx.Err()` checks the wrong one. Name each context after what it covers: `ctx`, `dialCtx`.

**6. Always call `cancel`, as soon as you're done.** It releases the timer. In a loop, call it directly; `defer` only runs when the function returns. `go vet` catches a missing `cancel`.

**7. A result that arrived is still a result.** Check `err != nil` before discarding: a dial that succeeded just as the scan stopped found a real open port.

**8. Test every way of stopping.** None of these bugs fail to compile or show up on a normal run. Run it to completion, press Ctrl-C, let the budget expire, and check the numbers add up.
