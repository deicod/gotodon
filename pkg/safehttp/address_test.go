package safehttp

import (
	"net/netip"
	"testing"
)

func TestIsPublicAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		// Public IPv4, including the edges of neighbouring special blocks.
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"93.184.215.14", true},
		{"100.63.255.255", true},
		{"100.128.0.0", true},
		{"172.15.255.255", true},
		{"172.32.0.0", true},
		{"192.31.196.1", true},
		{"198.17.255.255", true},
		{"198.20.0.0", true},
		{"223.255.255.255", true},

		// Non-public IPv4.
		{"0.0.0.0", false},
		{"0.1.2.3", false},
		{"10.0.0.1", false},
		{"100.64.0.1", false},
		{"100.100.100.200", false},
		{"127.0.0.1", false},
		{"127.255.255.254", false},
		{"169.254.169.254", false},
		{"172.16.0.1", false},
		{"172.31.255.255", false},
		{"192.0.0.1", false},
		{"192.0.2.1", false},
		{"192.88.99.1", false},
		{"192.168.1.1", false},
		{"198.18.0.1", false},
		{"198.19.255.255", false},
		{"198.51.100.1", false},
		{"203.0.113.1", false},
		{"224.0.0.1", false},
		{"239.255.255.250", false},
		{"240.0.0.1", false},
		{"255.255.255.255", false},

		// Public IPv6, and IPv6 forms that carry a public IPv4 address.
		{"2606:4700:4700::1111", true},
		{"2a00:1450:4001:82b::200e", true},
		{"2001:200::1", true},
		{"::ffff:8.8.8.8", true},
		{"64:ff9b::808:808", true},
		{"2002:808:808::1", true},

		// Non-public IPv6.
		{"::", false},
		{"::1", false},
		{"::127.0.0.1", false},
		{"::ffff:127.0.0.1", false},
		{"::ffff:10.0.0.1", false},
		{"::ffff:169.254.169.254", false},
		{"64:ff9b::7f00:1", false},
		{"64:ff9b::a9fe:a9fe", false},
		{"64:ff9b:1::1", false},
		{"100::1", false},
		{"2001::1", false},
		{"2001:2::1", false},
		{"2001:db8::1", false},
		{"2002:7f00:1::1", false},
		{"2002:a00:1::1", false},
		{"3fff::1", false},
		{"fc00::1", false},
		{"fd00:ec2::254", false},
		{"fe80::1", false},
		{"fe80::1%eth0", false},
		{"fec0::1", false},
		{"ff02::1", false},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := IsPublicAddr(netip.MustParseAddr(tt.addr)); got != tt.want {
				t.Fatalf("IsPublicAddr(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}

	if IsPublicAddr(netip.Addr{}) {
		t.Fatal("IsPublicAddr(zero Addr) = true, want false")
	}
}
