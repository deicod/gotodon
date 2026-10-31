package safehttp

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"syscall"
	"time"
)

const (
	// fallbackDelay is how long the preferred address family gets before the
	// other one is raced against it (RFC 8305), as with net.Dialer.
	fallbackDelay = 300 * time.Millisecond

	// minDialShare is the least time one address gets when several share the
	// remaining deadline, as with net.Dialer.
	minDialShare = 2 * time.Second

	// dialTimeout bounds one dial, name resolution and every connection
	// attempt together, when the request's deadline does not end it sooner.
	dialTimeout = 30 * time.Second
)

// requestDeadlineKey is the context key under which guardedTransport passes a
// request's deadline to its dial. http.Transport dials with a context that
// keeps the request's values but not its deadline.
type requestDeadlineKey struct{}

// LookupFunc resolves host to its addresses; network is "ip", "ip4" or "ip6".
type LookupFunc func(ctx context.Context, network, host string) ([]netip.Addr, error)

// DialFunc connects to address, an "ip:port" that has passed the address policy.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// dialer resolves host names itself and dials only the validated IP literals,
// so the address that is checked is the address that is connected to: a DNS
// answer that changes after the check (rebinding) is never consulted.
type dialer struct {
	lookup LookupFunc
	dial   DialFunc
}

func newDialer(lookup LookupFunc, dial DialFunc) *dialer {
	if lookup == nil {
		lookup = net.DefaultResolver.LookupNetIP
	}
	if dial == nil {
		dial = (&net.Dialer{
			KeepAlive:      30 * time.Second,
			ControlContext: checkDialedAddr,
		}).DialContext
	}
	return &dialer{lookup: lookup, dial: dial}
}

// DialContext implements http.Transport.DialContext.
func (d *dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	var ipNetwork string
	switch network {
	case "tcp":
		ipNetwork = "ip"
	case "tcp4":
		ipNetwork = "ip4"
	case "tcp6":
		ipNetwork = "ip6"
	default:
		return nil, fmt.Errorf("%w: network %q", ErrUnsafeURL, network)
	}

	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return nil, fmt.Errorf("%w: invalid port %q", ErrUnsafeURL, portText)
	}

	// One deadline covers the lookup and every connection attempt, so the
	// addresses share it (see dialOne) and the dial ends with its request
	// instead of carrying on in the background.
	deadline := time.Now().Add(dialTimeout)
	if requestDeadline, ok := ctx.Value(requestDeadlineKey{}).(time.Time); ok && requestDeadline.Before(deadline) {
		deadline = requestDeadline
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	addrs, err := d.resolve(ctx, ipNetwork, host)
	if err != nil {
		return nil, err
	}
	targets := make([]netip.AddrPort, len(addrs))
	for i, addr := range addrs {
		targets[i] = netip.AddrPortFrom(addr, uint16(port))
	}
	return d.dialParallel(ctx, network, targets)
}

// resolve returns the addresses to dial for host. The whole answer is refused
// when any address in it is not public: a name that mixes public and internal
// addresses is either misconfigured or trying to reach the internal one.
func (d *dialer) resolve(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		if !IsPublicAddr(addr) {
			return nil, forbiddenAddr(host, addr)
		}
		return []netip.Addr{addr.Unmap().WithZone("")}, nil
	}

	answer, err := d.lookup(ctx, network, host)
	if err != nil {
		return nil, err
	}
	addrs := make([]netip.Addr, 0, len(answer))
	for _, addr := range answer {
		if !IsPublicAddr(addr) {
			return nil, forbiddenAddr(host, addr)
		}
		addr = addr.Unmap().WithZone("")
		if (network == "ip4" && !addr.Is4()) || (network == "ip6" && !addr.Is6()) {
			continue
		}
		addrs = append(addrs, addr)
	}
	if len(addrs) == 0 {
		return nil, &net.DNSError{Err: "no suitable address found", Name: host, IsNotFound: true}
	}
	return addrs, nil
}

// dialParallel connects to the first reachable target. Targets of the
// preferred (first) address family are tried in order; if none has connected
// after fallbackDelay, or they have all failed, the other family is raced
// against them so a broken IPv6 path does not stall dual-stack hosts.
func (d *dialer) dialParallel(ctx context.Context, network string, targets []netip.AddrPort) (net.Conn, error) {
	var primaries, fallbacks []netip.AddrPort
	for _, target := range targets {
		if target.Addr().Is4() == targets[0].Addr().Is4() {
			primaries = append(primaries, target)
		} else {
			fallbacks = append(fallbacks, target)
		}
	}
	if len(fallbacks) == 0 {
		return d.dialSerial(ctx, network, primaries)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		conn    net.Conn
		err     error
		primary bool
	}
	results := make(chan result, 2)
	race := func(primary bool, targets []netip.AddrPort) {
		conn, err := d.dialSerial(ctx, network, targets)
		results <- result{conn: conn, err: err, primary: primary}
	}

	go race(true, primaries)
	running, fallbackStarted := 1, false
	startFallback := func() {
		if !fallbackStarted {
			fallbackStarted = true
			running++
			go race(false, fallbacks)
		}
	}
	timer := time.NewTimer(fallbackDelay)
	defer timer.Stop()

	var primaryErr, fallbackErr error
	for {
		select {
		case <-timer.C:
			startFallback()
		case res := <-results:
			running--
			if res.err == nil {
				if running > 0 {
					// The loser is cancelled on return; close its
					// connection if it won the race to connect anyway.
					go func() {
						if loser := <-results; loser.conn != nil {
							_ = loser.conn.Close()
						}
					}()
				}
				return res.conn, nil
			}
			if res.primary {
				primaryErr = res.err
			} else {
				fallbackErr = res.err
			}
			startFallback()
			if running == 0 {
				if primaryErr != nil {
					return nil, primaryErr
				}
				return nil, fallbackErr
			}
		}
	}
}

// dialSerial tries targets in order, giving each a fair share of the time
// left before ctx's deadline.
func (d *dialer) dialSerial(ctx context.Context, network string, targets []netip.AddrPort) (net.Conn, error) {
	var firstErr error
	for i, target := range targets {
		if err := ctx.Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		conn, err := d.dialOne(ctx, network, target, len(targets)-i)
		if err == nil {
			return conn, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

func (d *dialer) dialOne(ctx context.Context, network string, target netip.AddrPort, remaining int) (net.Conn, error) {
	if deadline, ok := ctx.Deadline(); ok && remaining > 1 {
		share := max(time.Until(deadline)/time.Duration(remaining), minDialShare)
		if partial := time.Now().Add(share); partial.Before(deadline) {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, partial)
			defer cancel()
		}
	}
	return d.dial(ctx, network, target.String())
}

// checkDialedAddr re-checks every socket's address just before it connects,
// so even a dial that bypassed resolve could not reach a non-public address.
func checkDialedAddr(_ context.Context, _, address string, _ syscall.RawConn) error {
	target, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: unexpected dial address %q", ErrForbiddenAddress, address)
	}
	if !IsPublicAddr(target.Addr()) {
		return forbiddenAddr(address, target.Addr())
	}
	return nil
}

func forbiddenAddr(host string, addr netip.Addr) error {
	if host == addr.String() {
		return fmt.Errorf("%w: %s", ErrForbiddenAddress, addr)
	}
	return fmt.Errorf("%w: %s is %s", ErrForbiddenAddress, host, addr)
}
