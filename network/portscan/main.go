package main

import (
	"context"
	"flag"
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
	budget := flag.Duration("budget", 0, "stop the whole scan after this long, e.g. 10s (0 = no limit)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: portscan [-budget DURATION] HOST|CIDR MIN_PORT MAX_PORT")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 3 {
		flag.Usage()
		os.Exit(2)
	}
	prefix, err := parsePrefix(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	minPort, err := strconv.Atoi(flag.Arg(1))
	if err != nil {
		log.Fatal(err)
	}
	maxPort, err := strconv.Atoi(flag.Arg(2))
	if err != nil {
		log.Fatal(err)
	}
	if minPort < 1 || maxPort > 65535 || minPort > maxPort {
		log.Fatal("ports must be 1-65535 and MIN_PORT <= MAX_PORT")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *budget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *budget)
		defer cancel()
	}

	const maxInFlight = 1000
	sem := make(chan struct{}, maxInFlight)

	var checked, open atomic.Int64
	start := time.Now()

	var wg sync.WaitGroup
scan:
	for host := prefix.Masked().Addr(); prefix.Contains(host); host = host.Next() {
		for port := minPort; port <= maxPort; port++ {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				break scan
			}
			wg.Go(func() {
				defer func() { <-sem }()
				addr := netip.AddrPortFrom(host, uint16(port))
				dialCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
				defer cancel()
				var dialer net.Dialer
				conn, err := dialer.DialContext(dialCtx, "tcp", addr.String())
				if err != nil && ctx.Err() != nil {
					return
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
	wg.Wait()
	status := "done"
	switch ctx.Err() {
	case context.Canceled:
		status = "interrupted"
	case context.DeadlineExceeded:
		status = "timeout"
	}

	log.Printf("%s: checked %d ports, %d open, in %v", status, checked.Load(), open.Load(), time.Since(start).Round(time.Millisecond))
}

// parsePrefix accepts one address ("10.0.0.1") or a range ("10.0.0.0/24").
func parsePrefix(s string) (netip.Prefix, error) {
	if addr, err := netip.ParseAddr(s); err == nil {
		return netip.PrefixFrom(addr, addr.BitLen()), nil
	}
	return netip.ParsePrefix(s)
}
