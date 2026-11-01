package elgato

import (
	"net"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func header(name string, typ dnsmessage.Type) dnsmessage.ResourceHeader {
	return dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET}
}

func pack(t *testing.T, response bool, answers, additionals []dnsmessage.Resource) []byte {
	t.Helper()

	msg := dnsmessage.Message{
		Header:      dnsmessage.Header{Response: response, Authoritative: true},
		Answers:     answers,
		Additionals: additionals,
	}

	b, err := msg.Pack()
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func ptr(instance string) dnsmessage.Resource {
	return dnsmessage.Resource{
		Header: header(ServiceName, dnsmessage.TypePTR),
		Body:   &dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(instance + "." + ServiceName)},
	}
}

func srv(instance, target string, port uint16) dnsmessage.Resource {
	return dnsmessage.Resource{
		Header: header(instance+"."+ServiceName, dnsmessage.TypeSRV),
		Body:   &dnsmessage.SRVResource{Target: dnsmessage.MustNewName(target), Port: port},
	}
}

func a(host string, ip [4]byte) dnsmessage.Resource {
	return dnsmessage.Resource{Header: header(host, dnsmessage.TypeA), Body: &dnsmessage.AResource{A: ip}}
}

func TestResponses(t *testing.T) {
	r := newResponses()
	src := net.IPv4(192, 168, 1, 99)

	// A complete response in a single packet.
	r.add(pack(t, true,
		[]dnsmessage.Resource{ptr("Elgato Key Light Air 1A2B")},
		[]dnsmessage.Resource{
			srv("Elgato Key Light Air 1A2B", "elgato-key-light-air-1a2b.local.", 9123),
			a("elgato-key-light-air-1a2b.local.", [4]byte{192, 168, 1, 20}),
		}), src)
	// Records split over several packets.
	r.add(pack(t, true, []dnsmessage.Resource{ptr("Elgato Key Light Air 3C4D")}, nil), src)
	r.add(pack(t, true, []dnsmessage.Resource{srv("Elgato Key Light Air 3C4D", "other.local.", 9124)}, nil), src)
	r.add(pack(t, true, []dnsmessage.Resource{a("other.local.", [4]byte{192, 168, 1, 21})}, nil), src)
	// A PTR with no SRV or A records falls back to the source and default port.
	r.add(pack(t, true, []dnsmessage.Resource{ptr("Elgato Key Light Air 5E6F")}, nil), net.IPv4(192, 168, 1, 22))
	// Queries and other services are ignored.
	r.add(pack(t, false, []dnsmessage.Resource{ptr("Not A Response")}, nil), src)
	r.add(pack(t, true, []dnsmessage.Resource{{
		Header: header("_http._tcp.local.", dnsmessage.TypePTR),
		Body:   &dnsmessage.PTRResource{PTR: dnsmessage.MustNewName("printer._http._tcp.local.")},
	}}, nil), src)
	r.add([]byte("garbage"), src)

	want := []Device{
		{Instance: "Elgato Key Light Air 1A2B", Host: "elgato-key-light-air-1a2b.local", IP: net.IPv4(192, 168, 1, 20), Port: 9123},
		{Instance: "Elgato Key Light Air 3C4D", Host: "other.local", IP: net.IPv4(192, 168, 1, 21), Port: 9124},
		{Instance: "Elgato Key Light Air 5E6F", IP: net.IPv4(192, 168, 1, 22), Port: 9123},
	}

	got := r.devices()
	if len(got) != len(want) {
		t.Fatalf("got %d devices, want %d: %+v", len(got), len(want), got)
	}

	for i := range want {
		if got[i].Instance != want[i].Instance || got[i].Host != want[i].Host || !got[i].IP.Equal(want[i].IP) || got[i].Port != want[i].Port {
			t.Errorf("device %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	if addr := got[1].Addr(); addr != "192.168.1.21:9124" {
		t.Errorf("Addr() = %q", addr)
	}
}

func TestDiscoveryQuery(t *testing.T) {
	b, err := discoveryQuery()
	if err != nil {
		t.Fatal(err)
	}

	var msg dnsmessage.Message
	if err := msg.Unpack(b); err != nil {
		t.Fatal(err)
	}

	if len(msg.Questions) != 1 || msg.Questions[0].Name.String() != ServiceName || msg.Questions[0].Type != dnsmessage.TypePTR {
		t.Errorf("unexpected query: %+v", msg)
	}
}
