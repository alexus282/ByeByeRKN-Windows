// Package wintunnel configures the Windows side of a tunnel: the adapter's addresses and MTU, DNS
// servers, the default route and the exception that keeps the WARP endpoint out of the tunnel.
//
// Everything goes through netsh and PowerShell, the same commands the Android app's equivalents
// produce, and every change is recorded so Teardown can undo it: a default route that outlives the
// process leaves the machine without internet.
package wintunnel

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
)

// Runner executes system commands. Tests pass a fake to see exactly what would run, because the
// real ones need administrator rights.
type Runner interface {
	Run(name string, args ...string) error
	Output(name string, args ...string) (string, error)
}

// Exec runs commands without opening a console window.
type Exec struct{}

func (Exec) Run(name string, args ...string) error {
	_, err := system(name, args...)
	return err
}

func (Exec) Output(name string, args ...string) (string, error) {
	return system(name, args...)
}

// system hides the window: a GUI app spawning netsh for every route would otherwise flicker.
func system(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.CombinedOutput()
	text := oemString(out)
	if err != nil {
		return "", fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(text))
	}
	return text, nil
}

// cpOEM is CP_OEMCP: netsh prints in the console's code page, which is not UTF-8 on a Russian
// Windows, and the error text is what the user sees in the log.
const cpOEM = 1

func oemString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	n, err := windows.MultiByteToWideChar(cpOEM, 0, &b[0], int32(len(b)), nil, 0)
	if err != nil || n <= 0 {
		return string(b)
	}
	buf := make([]uint16, n)
	if _, err := windows.MultiByteToWideChar(cpOEM, 0, &b[0], int32(len(b)), &buf[0], n); err != nil {
		return string(b)
	}
	return windows.UTF16ToString(buf)
}

const (
	family4 = "ipv4"
	family6 = "ipv6"
)

// Tunnel is one adapter's configuration. It is not safe for concurrent use of a single call, but
// the mutex keeps Teardown from racing a Configure.
type Tunnel struct {
	Runner Runner
	Logf   func(format string, args ...any)

	mu   sync.Mutex
	undo []step
	// pins caches the physical route to each endpoint: a search probes one address family many
	// times and the answer does not change during a connection attempt.
	pins map[string]physical
}

type step struct {
	what string
	args []string
}

type physical struct {
	Iface string `json:"iface"`
	Next  string `json:"next"`
}

func New(r Runner) *Tunnel {
	if r == nil {
		r = Exec{}
	}
	return &Tunnel{Runner: r, pins: make(map[string]physical)}
}

// Configure gives the adapter its addresses, MTU, metric and DNS servers. The metric matters: a
// route on this interface only wins over an existing VPN's default route when both are counted.
func (t *Tunnel) Configure(iface, v4, v6 string, mtu int, dns []string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pins = make(map[string]physical)

	for _, family := range []string{family4, family6} {
		// Wintun is point-to-point: there is no neighbour that could answer duplicate-address
		// detection. With DAD enabled, 172.16.0.2/32 stays Tentative and Windows reports "host
		// unreachable" despite having routes, because the interface has no usable source address.
		if err := t.do("interface "+family, "interface", family, "set", "interface", "interface="+iface,
			"metric=0", "dadtransmits=0", "routerdiscovery=disabled", "store=active"); err != nil {
			return err
		}
		// Windows enforces IPv6's protocol minimum of 1280 and rejects lower values. When the
		// IPv4 path needs a smaller MTU, leave IPv6 at the adapter default instead of aborting the
		// whole connection; the IPv4 tunnel is sufficient for the WARP session.
		if family == family6 && mtu < 1280 {
			continue
		}
		if err := t.do("mtu "+family, "interface", family, "set", "subinterface", iface, fmt.Sprintf("mtu=%d", mtu), "store=active"); err != nil {
			return err
		}
	}

	if v4 != "" {
		// The adapter is reused between runs, so the address may already be there; deleting it
		// first keeps Configure idempotent.
		t.Runner.Run("netsh", "interface", family4, "delete", "address", "name="+iface, "address="+v4, "store=active")
		if err := t.do("address "+v4, "interface", family4, "set", "address", "name="+iface, "static", v4, "255.255.255.255"); err != nil {
			return err
		}
	}
	if v6 != "" {
		t.Runner.Run("netsh", "interface", family6, "delete", "address", "interface="+iface, "address="+v6, "store=active")
		if err := t.do("address "+v6, "interface", family6, "set", "address", "interface="+iface, v6+"/128"); err != nil {
			return err
		}
	}

	return t.setDNS(iface, dns)
}

// setDNS puts the tunnel's resolvers on its own adapter. IPv4 and IPv6 servers live in separate
// lists, and each family takes its first server with "set" and the rest with indexed "add".
func (t *Tunnel) setDNS(iface string, servers []string) error {
	group := func(want6 bool) (list []string) {
		for _, s := range servers {
			if strings.Contains(s, ":") == want6 {
				list = append(list, s)
			}
		}
		return list
	}
	for _, family := range []struct {
		name string
		list []string
	}{{family4, group(false)}, {family6, group(true)}} {
		if len(family.list) == 0 {
			continue
		}
		if err := t.do("dns "+family.name, "interface", family.name, "set", "dnsservers", "name="+iface, "static", family.list[0], "register=none", "validate=no"); err != nil {
			return err
		}
		t.record("dns "+family.name, "interface", family.name, "set", "dnsservers", "name="+iface, "source=dhcp")
		for i, server := range family.list[1:] {
			if err := t.do("dns "+family.name, "interface", family.name, "add", "dnsservers", "name="+iface, server, fmt.Sprintf("index=%d", i+2), "validate=no"); err != nil {
				return err
			}
		}
	}
	return nil
}

// PinEndpoint adds a host route for the WARP endpoint through the interface that carries traffic
// now. Without it the default route added next sends the tunnel's own packets into itself.
func (t *Tunnel) PinEndpoint(iface, endpoint string) error {
	address, err := netip.ParseAddrPort(endpoint)
	if err != nil {
		return fmt.Errorf("wintunnel: %q is not an address:port: %w", endpoint, err)
	}
	host := address.Addr().String()
	family, prefix := family4, host+"/32"
	if address.Addr().Is6() {
		family, prefix = family6, host+"/128"
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	route, err := t.physicalRoute(host)
	if err != nil {
		return err
	}
	if route.Iface == iface {
		return fmt.Errorf("wintunnel: traffic to %s already goes through the tunnel", host)
	}
	next := route.Next
	if i := strings.IndexByte(next, '%'); i >= 0 {
		next = next[:i] // a link-local gateway in netsh form takes no zone when the interface is given
	}
	if next == "" {
		next = "0.0.0.0"
		if family == family6 {
			next = "::"
		}
	}
	if err := t.do("pin "+host, "interface", family, "add", "route", prefix, route.Iface, next, "metric=1", "store=active"); err != nil {
		return err
	}
	t.record("unpin "+host, "interface", family, "delete", "route", prefix, route.Iface, "store=active")
	return nil
}

// DefaultRoute makes the tunnel the way out for both address families. Two /1 routes cover the
// same addresses as /0 but always win over another VPN's default route, regardless of interface
// metrics. A /32 Wintun address has no real gateway, so the unspecified next hop means on-link.
func (t *Tunnel) DefaultRoute(iface string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, route := range []struct {
		family   string
		prefixes []string
		next     string
	}{
		{family4, []string{"0.0.0.0/1", "128.0.0.0/1"}, "0.0.0.0"},
		{family6, []string{"::/1", "8000::/1"}, "::"},
	} {
		for _, prefix := range route.prefixes {
			if err := t.do("route "+prefix, "interface", route.family, "add", "route", prefix, iface, route.next, "metric=0", "store=active"); err != nil {
				return err
			}
			t.record("route "+prefix, "interface", route.family, "delete", "route", prefix, iface, "store=active")
		}
	}
	return nil
}

// Teardown removes everything Configure, PinEndpoint and DefaultRoute added, newest first.
func (t *Tunnel) Teardown() {
	t.mu.Lock()
	steps := t.undo
	t.undo = nil
	t.pins = make(map[string]physical)
	t.mu.Unlock()

	for i := len(steps) - 1; i >= 0; i-- {
		if err := t.Runner.Run("netsh", steps[i].args...); err != nil {
			t.logf("teardown %s: %v", steps[i].what, err)
		}
	}
}

func (t *Tunnel) do(what string, args ...string) error {
	if err := t.Runner.Run("netsh", args...); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

func (t *Tunnel) record(what string, args ...string) {
	t.undo = append(t.undo, step{what, args})
}

// routeScript asks Windows which interface and gateway would carry a packet to the endpoint.
// Find-NetRoute returns both the address and the matching route, so the route is the entry with a
// destination prefix.
const routeScript = `$r = Find-NetRoute -RemoteIPAddress '%s' -ErrorAction Stop |
  Where-Object { $_.DestinationPrefix } | Select-Object -First 1
if (-not $r) { exit 1 }
@{ iface = $r.InterfaceAlias; next = $r.NextHop } | ConvertTo-Json -Compress`

func (t *Tunnel) physicalRoute(host string) (physical, error) {
	if route, ok := t.pins[host]; ok {
		return route, nil
	}
	out, err := t.Runner.Output("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", fmt.Sprintf(routeScript, host))
	if err != nil {
		return physical{}, fmt.Errorf("find route to %s: %w", host, err)
	}
	var route physical
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &route); err != nil {
		return physical{}, fmt.Errorf("find route to %s: %v (%q)", host, err, strings.TrimSpace(out))
	}
	if route.Iface == "" {
		return physical{}, fmt.Errorf("find route to %s: no interface", host)
	}
	t.pins[host] = route
	return route, nil
}

// Diagnose captures what Windows actually configured, while the adapter and routes still exist.
// netsh accepting a command does not prove that route selection will use it.
func (t *Tunnel) Diagnose(iface, host string) string {
	script := fmt.Sprintf(`$ErrorActionPreference = 'SilentlyContinue'
$iface = Get-NetIPInterface -InterfaceAlias '%s'
$addr = Get-NetIPAddress -InterfaceAlias '%s'
$routes = Get-NetRoute -InterfaceAlias '%s'
$selected = Find-NetRoute -RemoteIPAddress '%s' | Where-Object { $_.DestinationPrefix } | Select-Object -First 1
@{
  interfaces = @($iface | Select-Object InterfaceIndex,AddressFamily,ConnectionState,NlMtu,InterfaceMetric,AutomaticMetric)
  addresses = @($addr | Select-Object IPAddress,PrefixLength,AddressState,SkipAsSource)
  routes = @($routes | Select-Object DestinationPrefix,NextHop,RouteMetric,State)
  selected = @($selected | Select-Object InterfaceAlias,InterfaceIndex,DestinationPrefix,NextHop,RouteMetric)
} | ConvertTo-Json -Depth 5 -Compress`, iface, iface, iface, host)
	out, err := t.Runner.Output("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(out)
}

func (t *Tunnel) logf(format string, args ...any) {
	if t.Logf != nil {
		t.Logf(format, args...)
	}
}
