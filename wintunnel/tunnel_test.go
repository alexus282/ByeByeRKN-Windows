package wintunnel

import (
	"errors"
	"strings"
	"testing"
)

type call struct {
	name string
	args []string
}

type fakeRunner struct {
	calls []call
	route string
	// failOn rejects a command as an unelevated session would.
	failOn func(args []string) bool
}

func (f *fakeRunner) Run(name string, args ...string) error {
	f.calls = append(f.calls, call{name, args})
	if f.failOn != nil && f.failOn(args) {
		return errors.New("access denied")
	}
	return nil
}

func (f *fakeRunner) Output(name string, args ...string) (string, error) {
	f.calls = append(f.calls, call{name, args})
	if f.route == "" {
		return "", errors.New("no route")
	}
	return f.route, nil
}

func argsText(c call) string { return c.name + " " + strings.Join(c.args, " ") }

func find(calls []call, want string) int {
	for i, c := range calls {
		if strings.HasPrefix(argsText(c), want) {
			return i
		}
	}
	return -1
}

func requireCall(t *testing.T, calls []call, want string) {
	t.Helper()
	if find(calls, want) < 0 {
		t.Fatalf("no call %q in:\n%s", want, dump(calls))
	}
}

func dump(calls []call) string {
	var b strings.Builder
	for _, c := range calls {
		b.WriteString(argsText(c))
		b.WriteString("\n")
	}
	return b.String()
}

func TestConfigureGivesTheAdapterItsAddressesDNSAndMTU(t *testing.T) {
	f := &fakeRunner{}
	tun := New(f)
	if err := tun.Configure("ByeByeRKN", "172.16.0.2", "2606:4700:110::1", 1280,
		[]string{"1.1.1.1", "1.0.0.1", "2606:4700:4700::1111"}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		// The metric decides whether the tunnel's default route beats an existing VPN's.
		"netsh interface ipv4 set interface interface=ByeByeRKN metric=0 dadtransmits=0 routerdiscovery=disabled store=active",
		"netsh interface ipv6 set interface interface=ByeByeRKN metric=0 dadtransmits=0 routerdiscovery=disabled store=active",
		"netsh interface ipv4 set subinterface ByeByeRKN mtu=1280 store=active",
		"netsh interface ipv4 set address name=ByeByeRKN static 172.16.0.2 255.255.255.255",
		"netsh interface ipv6 set address interface=ByeByeRKN 2606:4700:110::1/128",
		"netsh interface ipv4 set dnsservers name=ByeByeRKN static 1.1.1.1 register=none validate=no",
		"netsh interface ipv4 add dnsservers name=ByeByeRKN 1.0.0.1 index=2 validate=no",
		"netsh interface ipv6 set dnsservers name=ByeByeRKN static 2606:4700:4700::1111 register=none validate=no",
	} {
		requireCall(t, f.calls, want)
	}
}

func TestConfigureDoesNotSetAnIllegalIPv6MTU(t *testing.T) {
	f := &fakeRunner{}
	if err := New(f).Configure("ByeByeRKN", "172.16.0.2", "2606:4700:110::1", 1200, nil); err != nil {
		t.Fatal(err)
	}
	requireCall(t, f.calls, "netsh interface ipv4 set subinterface ByeByeRKN mtu=1200 store=active")
	if find(f.calls, "netsh interface ipv6 set subinterface") >= 0 {
		t.Fatal("Windows rejects IPv6 MTU values below 1280")
	}
}

func TestDefaultRouteCoversEverythingWithTwoMoreSpecificRoutes(t *testing.T) {
	f := &fakeRunner{}
	if err := New(f).DefaultRoute("ByeByeRKN"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"netsh interface ipv4 add route 0.0.0.0/1 ByeByeRKN 0.0.0.0 metric=0 store=active",
		"netsh interface ipv4 add route 128.0.0.0/1 ByeByeRKN 0.0.0.0 metric=0 store=active",
		"netsh interface ipv6 add route ::/1 ByeByeRKN :: metric=0 store=active",
		"netsh interface ipv6 add route 8000::/1 ByeByeRKN :: metric=0 store=active",
	} {
		requireCall(t, f.calls, want)
	}
}

func TestTeardownUndoesRoutesBeforeRestoringDNS(t *testing.T) {
	f := &fakeRunner{route: `{"iface":"Ethernet 3","next":"10.10.12.1"}`}
	tun := New(f)
	if err := tun.Configure("ByeByeRKN", "172.16.0.2", "", 1280, []string{"1.1.1.1"}); err != nil {
		t.Fatal(err)
	}
	if err := tun.PinEndpoint("ByeByeRKN", "162.159.192.1:2408"); err != nil {
		t.Fatal(err)
	}
	if err := tun.DefaultRoute("ByeByeRKN"); err != nil {
		t.Fatal(err)
	}

	f.calls = nil
	tun.Teardown()

	// The default routes go first: while they are there, the pinned route is the only way the
	// teardown's own traffic could still leave.
	default4 := find(f.calls, "netsh interface ipv4 delete route 128.0.0.0/1 ByeByeRKN")
	unpin := find(f.calls, "netsh interface ipv4 delete route 162.159.192.1/32 Ethernet 3")
	dns := find(f.calls, "netsh interface ipv4 set dnsservers name=ByeByeRKN source=dhcp")
	if default4 < 0 || unpin < 0 || dns < 0 {
		t.Fatalf("incomplete teardown:\n%s", dump(f.calls))
	}
	if !(default4 < unpin && unpin < dns) {
		t.Fatalf("wrong order:\n%s", dump(f.calls))
	}
}

func TestPinEndpointPinsThroughThePhysicalInterface(t *testing.T) {
	f := &fakeRunner{route: `{"iface":"Ethernet 3","next":"10.10.12.1"}`}
	tun := New(f)
	if err := tun.PinEndpoint("ByeByeRKN", "162.159.192.1:2408"); err != nil {
		t.Fatal(err)
	}
	requireCall(t, f.calls, "netsh interface ipv4 add route 162.159.192.1/32 Ethernet 3 10.10.12.1 metric=1 store=active")
}

func TestPinEndpointHandlesIPv6AndOnLinkGateways(t *testing.T) {
	f := &fakeRunner{route: `{"iface":"Ethernet","next":"::"}`}
	tun := New(f)
	if err := tun.PinEndpoint("ByeByeRKN", "[2606:4700:d0::a29f:c001]:2408"); err != nil {
		t.Fatal(err)
	}
	requireCall(t, f.calls, "netsh interface ipv6 add route 2606:4700:d0::a29f:c001/128 Ethernet :: metric=1 store=active")
}

func TestPinEndpointRefusesToRouteThroughItself(t *testing.T) {
	f := &fakeRunner{route: `{"iface":"ByeByeRKN","next":"0.0.0.0"}`}
	tun := New(f)
	if err := tun.PinEndpoint("ByeByeRKN", "162.159.192.1:2408"); err == nil {
		t.Fatal("a route through the tunnel must be refused: it would be a loop")
	}
}

func TestConfigureFailsWhenWindowsRefuses(t *testing.T) {
	f := &fakeRunner{failOn: func(args []string) bool {
		return strings.Contains(strings.Join(args, " "), "set address")
	}}
	tun := New(f)
	if err := tun.Configure("ByeByeRKN", "172.16.0.2", "", 1280, nil); err == nil {
		t.Fatal("an unelevated session must not report success")
	}
}
