package catalog

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
)

// V6Family is the family every IPv6 endpoint belongs to.
const V6Family = "v6"

// Every address of a family's /24 is an endpoint (measured: 10 of 10 random hosts in each family
// answered). The first ten are what every public config generator hands out.
const (
	firstHost = 1
	lastHost  = 254
)

const maxDrawsPerEndpoint = 20

// Sample picks count endpoints across every address family, because the data centre a route exits
// from depends on the family. About one in five is IPv6, free to probe: unreachable ones fail
// instantly. Well-known addresses always come first.
func (c *Catalog) Sample(count int, rnd *rand.Rand) []string {
	if rnd == nil {
		rnd = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}
	var result []string
	add := func(e string) {
		if !slices.Contains(result, e) {
			result = append(result, e)
		}
	}
	// Names would need DNS, which a scan must not wait for; a catalog may still carry one.
	for _, e := range c.WellKnown {
		if IsLiteral(HostOf(e)) {
			add(e)
		}
	}
	families := slices.Clone(c.Prefixes)
	rnd.Shuffle(len(families), func(i, j int) { families[i], families[j] = families[j], families[i] })
	next := 0
	// A small downloaded catalog may hold fewer distinct endpoints than asked for.
	for draws := 0; len(result) < count && draws < count*maxDrawsPerEndpoint; draws++ {
		port := c.Ports[rnd.IntN(len(c.Ports))]
		host := firstHost + rnd.IntN(lastHost-firstHost+1)
		if len(c.V6Prefixes) > 0 && rnd.IntN(5) == 0 {
			add(fmt.Sprintf("[%s%02x]:%d", c.V6Prefixes[rnd.IntN(len(c.V6Prefixes))], host, port))
		} else {
			add(families[next%len(families)] + strconv.Itoa(host) + ":" + strconv.Itoa(port))
			next++
		}
	}
	return result
}

// FamilyOf names the address family of an endpoint: routes in one family tend to share an exit,
// so results are judged per family.
func FamilyOf(endpoint string) string {
	if strings.HasPrefix(endpoint, "[") {
		return V6Family
	}
	host := HostOf(endpoint)
	if i := strings.LastIndexByte(host, '.'); i >= 0 && IsLiteral(host) {
		return host[:i+1]
	}
	return host
}

// HostOf returns the address or name of "host:port" or "[v6]:port", without brackets.
func HostOf(endpoint string) string {
	i := strings.LastIndexByte(endpoint, ':')
	if i <= 0 {
		return endpoint
	}
	return strings.TrimSuffix(strings.TrimPrefix(endpoint[:i], "["), "]")
}

func PortOf(endpoint string) int {
	p, _ := strconv.Atoi(endpoint[strings.LastIndexByte(endpoint, ':')+1:])
	return p
}

// IsLiteral reports whether host is an IPv4 or IPv6 address rather than a name: resolving it never
// touches DNS.
func IsLiteral(host string) bool {
	if strings.Contains(host, ":") {
		return true
	}
	if host == "" {
		return false
	}
	for _, r := range host {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}
