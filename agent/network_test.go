package agent

import (
	"net/netip"
	"testing"
)

func TestPublicAddressClassification(t *testing.T) {
	tests := []struct {
		address string
		public  bool
	}{
		{address: "8.8.8.8", public: true},
		{address: "2001:4860:4860::8888", public: true},
		{address: "10.0.0.1", public: false},
		{address: "127.0.0.1", public: false},
		{address: "169.254.169.254", public: false},
		{address: "192.0.2.1", public: false},
		{address: "::1", public: false},
		{address: "fc00::1", public: false},
		{address: "fe80::1", public: false},
	}
	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			if actual := isPublicAddress(netip.MustParseAddr(test.address)); actual != test.public {
				t.Fatalf("isPublicAddress(%s) = %t, want %t", test.address, actual, test.public)
			}
		})
	}
}
