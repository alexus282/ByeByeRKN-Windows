package probe

import (
	"net"
	"sort"
	"sync"
	"time"

	"github.com/alexus282/ByeByeRKN-Windows/awg"
	"github.com/alexus282/ByeByeRKN-Windows/catalog"
)

// Host is the WARP endpoint name; the catalog's addresses answer under it.
const Host = "engage.cloudflareclient.com"

// Resolve turns "host:port" into an address. A scan must not wait for DNS, so the catalog only
// hands out literal addresses, and this only has to resolve them.
func Resolve(endpoint string) (*net.UDPAddr, error) {
	return net.ResolveUDPAddr("udp", endpoint)
}

// Route is one pair a probe tries: an endpoint with a concrete obfuscation setup.
type Route struct {
	Endpoint string
	Params   awg.Params
}

// Hit is a route that answered a handshake, with the round trip it took.
type Hit struct {
	Route
	RTT time.Duration
}

// Scanner probes routes in parallel and keeps the ones that answered, fastest first.
type Scanner struct {
	Probe *Probe
	// Resolve maps an endpoint to an address; the app passes one that looks names up outside
	// the tunnel. Defaults to [Resolve].
	Resolve func(endpoint string) (*net.UDPAddr, error)
	// Parallelism is how many probes fly at once; Timeout applies to each route.
	Parallelism int
	Timeout     time.Duration
}

func NewScanner(probe *Probe) *Scanner {
	return &Scanner{Probe: probe, Resolve: Resolve, Parallelism: 24, Timeout: 1500 * time.Millisecond}
}

// Scan tries every endpoint with every obfuscation variant.
func (s *Scanner) Scan(endpoints []string, variants []awg.Params, progress func(done, total, found int)) []Hit {
	routes := make([]Route, 0, len(endpoints)*len(variants))
	for _, endpoint := range endpoints {
		for _, params := range variants {
			routes = append(routes, Route{Endpoint: endpoint, Params: params})
		}
	}
	return s.ScanRoutes(routes, progress)
}

// ScanRoutes probes specific routes, such as the ones remembered for this network.
func (s *Scanner) ScanRoutes(routes []Route, progress func(done, total, found int)) []Hit {
	resolve := s.Resolve
	if resolve == nil {
		resolve = Resolve
	}
	parallelism := s.Parallelism
	if parallelism <= 0 {
		parallelism = 24
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}

	var (
		mu    sync.Mutex
		hits  []Hit
		done  int
		wg    sync.WaitGroup
		take  = make(chan struct{}, parallelism)
		total = len(routes)
	)
	for _, route := range routes {
		wg.Add(1)
		take <- struct{}{}
		go func(route Route) {
			defer wg.Done()
			defer func() { <-take }()

			address, err := resolve(route.Endpoint)
			var rtt time.Duration
			if err == nil {
				rtt, err = s.Probe.Probe(address, route.Params, timeout)
			}
			mu.Lock()
			defer mu.Unlock()
			done++
			if err == nil {
				hits = append(hits, Hit{Route: route, RTT: rtt})
			}
			if progress != nil {
				progress(done, total, len(hits))
			}
		}(route)
	}
	wg.Wait()

	sort.Slice(hits, func(i, j int) bool { return hits[i].RTT < hits[j].RTT })
	return hits
}

// Sample draws endpoints across every address family from the current catalog, well-known
// addresses first, exactly as the Android app's scan starts.
func Sample(count int) []string {
	return catalog.Current().Sample(count, nil)
}
