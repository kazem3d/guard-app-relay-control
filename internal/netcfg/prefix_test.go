package netcfg

import "testing"

func TestNetmaskToPrefix(t *testing.T) {
	cases := []struct {
		netmask string
		want    uint32
		wantErr bool
	}{
		{"255.255.255.0", 24, false},
		{"255.255.255.255", 32, false},
		{"255.255.254.0", 23, false},
		{"255.255.0.0", 16, false},
		{"0.0.0.0", 0, false},
		{"255.0.255.0", 0, true}, // non-contiguous
		{"not-an-ip", 0, true},
		{"", 0, true},
	}
	for _, c := range cases {
		got, err := netmaskToPrefix(c.netmask)
		if c.wantErr {
			if err == nil {
				t.Errorf("netmaskToPrefix(%q): expected error, got %d", c.netmask, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("netmaskToPrefix(%q): unexpected error: %v", c.netmask, err)
			continue
		}
		if got != c.want {
			t.Errorf("netmaskToPrefix(%q) = %d, want %d", c.netmask, got, c.want)
		}
	}
}

func TestPrefixToNetmask(t *testing.T) {
	cases := []struct {
		prefix  uint32
		want    string
		wantErr bool
	}{
		{24, "255.255.255.0", false},
		{32, "255.255.255.255", false},
		{23, "255.255.254.0", false},
		{16, "255.255.0.0", false},
		{0, "0.0.0.0", false},
		{33, "", true},
	}
	for _, c := range cases {
		got, err := prefixToNetmask(c.prefix)
		if c.wantErr {
			if err == nil {
				t.Errorf("prefixToNetmask(%d): expected error, got %q", c.prefix, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("prefixToNetmask(%d): unexpected error: %v", c.prefix, err)
			continue
		}
		if got != c.want {
			t.Errorf("prefixToNetmask(%d) = %q, want %q", c.prefix, got, c.want)
		}
	}
}

// TestPrefixRoundTrip guards against off-by-one errors across the whole
// range, since those are exactly the kind of bug that's silent until a
// misconfigured static IP locks someone out of a headless box.
func TestPrefixRoundTrip(t *testing.T) {
	for p := uint32(0); p <= 32; p++ {
		netmask, err := prefixToNetmask(p)
		if err != nil {
			t.Fatalf("prefixToNetmask(%d): %v", p, err)
		}
		back, err := netmaskToPrefix(netmask)
		if err != nil {
			t.Fatalf("netmaskToPrefix(%q): %v", netmask, err)
		}
		if back != p {
			t.Errorf("round trip failed: %d -> %q -> %d", p, netmask, back)
		}
	}
}
