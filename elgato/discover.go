package elgato

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/ipv4"
)

// ServiceName is the DNS-SD service type advertised by Elgato lights.
const ServiceName = "_elg._tcp.local."

// queryInterval is how often the discovery query is resent while waiting
// for responses, in case packets are lost.
const queryInterval = time.Second

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// Device is a light found on the local network by Discover.
type Device struct {
	// Instance is the DNS-SD instance name, e.g. "Elgato Key Light Air 1A2B".
	Instance string
	// Host is the mDNS hostname of the device, e.g. "elgato-key-light-air-1a2b.local".
	Host string
	IP   net.IP
	Port int
}

// Addr returns the ip:port the device's API can be reached on.
func (d Device) Addr() string {
	return net.JoinHostPort(d.IP.String(), strconv.Itoa(d.Port))
}

// Discover finds Elgato lights on the local network using multicast DNS
// service discovery, waiting up to timeout for responses.
//
// Queries are sent from two sockets, as which one receives the responses
// depends on the network and the local firewall:
//
//   - one bound to the mDNS port, shared with any mDNS daemon on this
//     machine, which receives the responses multicast to the group. Firewalls
//     that allow the mDNS service let these through.
//   - one bound to an ephemeral port, which responders reply to directly
//     (RFC 6762 section 6.7). This works when the mDNS port can't be shared,
//     but stateful firewalls may drop the replies.
func Discover(ctx context.Context, timeout time.Duration) ([]Device, error) {
	query, err := discoveryQuery()
	if err != nil {
		return nil, err
	}

	ifaces := multicastInterfaces()

	var conns []*net.UDPConn
	if conn, err := listenMulticast(ctx, ifaces); err == nil {
		conns = append(conns, conn)
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err == nil {
		conns = append(conns, conn)
	}

	if len(conns) == 0 {
		return nil, fmt.Errorf("opening mDNS socket: %w", err)
	}

	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, timeout)
	packets := make(chan packet)

	var wg sync.WaitGroup
	for _, conn := range conns {
		wg.Go(func() { readPackets(ctx, conn, packets) })
	}

	defer func() {
		cancel()

		for _, conn := range conns {
			_ = conn.Close()
		}

		wg.Wait()
	}()

	send := func() error {
		var err error

		for _, conn := range conns {
			sendErr := sendQuery(conn, ifaces, query)
			if sendErr == nil {
				return nil
			}

			err = sendErr
		}

		return err
	}
	if err := send(); err != nil {
		return nil, fmt.Errorf("sending mDNS query: %w", err)
	}

	ticker := time.NewTicker(queryInterval)
	defer ticker.Stop()

	r := newResponses()

	for {
		select {
		case p := <-packets:
			r.add(p.data, p.src)
		case <-ticker.C:
			if err := send(); err != nil {
				return nil, fmt.Errorf("resending mDNS query: %w", err)
			}
		case <-ctx.Done():
			if err := parent.Err(); err != nil {
				return nil, err
			}

			return r.devices(), nil
		}
	}
}

type packet struct {
	data []byte
	src  net.IP
}

func readPackets(ctx context.Context, conn *net.UDPConn, packets chan<- packet) {
	buf := make([]byte, 9000)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}

		select {
		case packets <- packet{data: slices.Clone(buf[:n]), src: src.IP}:
		case <-ctx.Done():
			return
		}
	}
}

// listenMulticast opens a socket on the mDNS port that has joined the mDNS
// group on each of the interfaces.
func listenMulticast(ctx context.Context, ifaces []net.Interface) (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: reusePort}

	c, err := lc.ListenPacket(ctx, "udp4", fmt.Sprintf("0.0.0.0:%d", mdnsGroup.Port))
	if err != nil {
		return nil, err
	}

	conn, ok := c.(*net.UDPConn)
	if !ok {
		_ = c.Close()
		return nil, errors.New("mDNS listener is not a UDP connection")
	}

	pc := ipv4.NewPacketConn(conn)
	joined := false

	for _, ifi := range ifaces {
		if err := pc.JoinGroup(&ifi, &net.UDPAddr{IP: mdnsGroup.IP}); err == nil {
			joined = true
		}
	}

	if !joined {
		_ = conn.Close()
		return nil, errors.New("could not join the mDNS group on any interface")
	}

	return conn, nil
}

// sendQuery multicasts the query on each of the interfaces.
func sendQuery(conn *net.UDPConn, ifaces []net.Interface, query []byte) error {
	pc := ipv4.NewPacketConn(conn)
	if err := pc.SetMulticastTTL(255); err != nil {
		return err
	}

	sent := false

	for _, ifi := range ifaces {
		if err := pc.SetMulticastInterface(&ifi); err != nil {
			continue
		}

		if _, err := conn.WriteTo(query, mdnsGroup); err == nil {
			sent = true
		}
	}

	if !sent {
		// Fall back to the default multicast route.
		_, err := conn.WriteTo(query, mdnsGroup)
		return err
	}

	return nil
}

func discoveryQuery() ([]byte, error) {
	name, err := dnsmessage.NewName(ServiceName)
	if err != nil {
		return nil, err
	}

	msg := dnsmessage.Message{
		Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET}},
	}

	return msg.Pack()
}

func multicastInterfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var ifaces []net.Interface

	for _, ifi := range all {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}

		if slices.ContainsFunc(addrs, func(a net.Addr) bool {
			ipn, ok := a.(*net.IPNet)
			return ok && ipn.IP.To4() != nil
		}) {
			ifaces = append(ifaces, ifi)
		}
	}

	return ifaces
}

type srvRecord struct {
	target string
	port   int
}

// responses accumulates records from mDNS responses. A device's PTR, SRV and
// A records may arrive in the same packet or across several.
type responses struct {
	instances map[string]net.IP // instance name -> source of the response
	srv       map[string]srvRecord
	addrs     map[string]net.IP
}

func newResponses() *responses {
	return &responses{
		instances: map[string]net.IP{},
		srv:       map[string]srvRecord{},
		addrs:     map[string]net.IP{},
	}
}

func (r *responses) add(packet []byte, src net.IP) {
	var msg dnsmessage.Message
	if err := msg.Unpack(packet); err != nil || !msg.Response {
		return
	}

	for _, rr := range slices.Concat(msg.Answers, msg.Additionals) {
		name := strings.ToLower(rr.Header.Name.String())
		switch body := rr.Body.(type) {
		case *dnsmessage.PTRResource:
			if name == ServiceName {
				r.instances[body.PTR.String()] = src
			}
		case *dnsmessage.SRVResource:
			r.srv[name] = srvRecord{target: strings.ToLower(body.Target.String()), port: int(body.Port)}
		case *dnsmessage.AResource:
			r.addrs[name] = net.IP(body.A[:])
		default:
		}
	}
}

func (r *responses) devices() []Device {
	var devices []Device

	for instance, src := range r.instances {
		d := Device{
			Instance: strings.TrimSuffix(instance, "."+ServiceName),
			IP:       src,
			Port:     9123,
		}
		if s, ok := r.srv[strings.ToLower(instance)]; ok {
			d.Host = strings.TrimSuffix(s.target, ".")

			d.Port = s.port
			if ip, ok := r.addrs[s.target]; ok {
				d.IP = ip
			}
		}

		devices = append(devices, d)
	}

	slices.SortFunc(devices, func(a, b Device) int { return strings.Compare(a.Instance, b.Instance) })

	return devices
}
