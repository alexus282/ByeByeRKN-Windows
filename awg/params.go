// Package awg builds the AmneziaWG obfuscation settings the app tries, the same presets as the
// Android app: only client-side junk (Jc/Jmin/Jmax and I1) is used, S1/S2 stay 0 and H1..H4 keep
// the standard message types, otherwise Cloudflare would not understand the handshake.
package awg

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/alexus282/ByeByeRKN/windows/catalog"
)

type Mask int

const (
	Light Mask = iota
	QUIC
	STUN
	DNS
	SIP
	Heavy
	None
)

// AutoOrder is the order the automatic search tries: the best disguises first, then ever more
// desperate ones.
var AutoOrder = []Mask{QUIC, STUN, Light, DNS, SIP, Heavy, None}

type firstPacket int

const (
	noFirst firstPacket = iota
	quicFirst
	dnsFirst
	stunFirst
	sipFirst
)

// Junk sizes are ranges: every generated config gets its own numbers, so there is no fixed
// pattern for DPI to learn.
type spec struct {
	name                       string
	junkCount, junkMin, spread [2]int
	first                      firstPacket
}

var specs = map[Mask]spec{
	Light: {"LIGHT", [2]int{3, 6}, [2]int{40, 64}, [2]int{20, 60}, noFirst},
	QUIC:  {"QUIC", [2]int{3, 6}, [2]int{40, 64}, [2]int{20, 60}, quicFirst},
	STUN:  {"STUN", [2]int{3, 6}, [2]int{40, 64}, [2]int{20, 60}, stunFirst},
	DNS:   {"DNS", [2]int{3, 6}, [2]int{40, 64}, [2]int{20, 60}, dnsFirst},
	SIP:   {"SIP", [2]int{3, 6}, [2]int{40, 64}, [2]int{20, 60}, sipFirst},
	Heavy: {"HEAVY", [2]int{60, 120}, [2]int{20, 40}, [2]int{300, 800}, noFirst},
	None:  {"NONE", [2]int{0, 0}, [2]int{0, 0}, [2]int{0, 0}, noFirst},
}

// maxJunk is the largest junk packet AmneziaWG accepts.
const maxJunk = 1280

func (m Mask) String() string { return specs[m].name }

func ParseMask(name string) (Mask, bool) {
	for m, s := range specs {
		if s.name == name {
			return m, true
		}
	}
	return None, false
}

// Params is a concrete obfuscation setup: the preset, its randomized junk sizes and the generated I1.
type Params struct {
	Mask Mask
	Jc   int
	Jmin int
	Jmax int
	I1   []byte
}

// NewParams generates a fresh setup for mask; sni is the site the QUIC disguise shows, or empty
// for a random one from the catalog.
func NewParams(m Mask, sni string) Params {
	s := specs[m]
	jc := pick(s.junkCount)
	jmin, jmax := 0, 0
	if jc > 0 {
		jmin = pick(s.junkMin)
		jmax = min(jmin+pick(s.spread), maxJunk)
	}
	var i1 []byte
	switch s.first {
	case quicFirst:
		if strings.TrimSpace(sni) == "" {
			sni = RandomSNI()
		}
		i1 = QuicInitial(sni)
	case dnsFirst:
		i1 = DNSQuery("")
	case stunFirst:
		i1 = STUNBindingRequest()
	case sipFirst:
		i1 = []byte(SIPOptions())
	}
	return Params{Mask: m, Jc: jc, Jmin: jmin, Jmax: jmax, I1: i1}
}

// RandomSNI picks one of the popular sites the catalog lists.
func RandomSNI() string {
	list := catalog.Current().QuicSNI
	return list[randInt(len(list))]
}

// UAPI renders the obfuscation keys of amneziawg-go's configuration protocol. Every key is always
// set, so a running device switching from one preset to another keeps nothing of the old one.
func (p Params) UAPI() string {
	var b strings.Builder
	fmt.Fprintf(&b, "jc=%d\njmin=%d\njmax=%d\ns1=0\ns2=0\nh1=1\nh2=2\nh3=3\nh4=4\n", p.Jc, p.Jmin, p.Jmax)
	if p.I1 != nil {
		fmt.Fprintf(&b, "i1=<b 0x%s>\n", hex.EncodeToString(p.I1))
	}
	return b.String()
}

// ConfigLines renders the same settings in wg-quick format, as AmneziaWG clients import them.
func (p Params) ConfigLines() []string {
	var lines []string
	if p.Jc > 0 {
		lines = append(lines, fmt.Sprintf("Jc = %d", p.Jc), fmt.Sprintf("Jmin = %d", p.Jmin), fmt.Sprintf("Jmax = %d", p.Jmax))
	}
	if p.Jc > 0 || p.I1 != nil {
		lines = append(lines, "S1 = 0", "S2 = 0", "H1 = 1", "H2 = 2", "H3 = 3", "H4 = 4")
	}
	if p.I1 != nil {
		lines = append(lines, "I1 = <b 0x"+hex.EncodeToString(p.I1)+">")
	}
	return lines
}

type paramsJSON struct {
	Mask string `json:"obfuscation"`
	Jc   int    `json:"jc"`
	Jmin int    `json:"jmin"`
	Jmax int    `json:"jmax"`
	I1   string `json:"i1,omitempty"`
}

func (p Params) MarshalJSON() ([]byte, error) {
	return json.Marshal(paramsJSON{p.Mask.String(), p.Jc, p.Jmin, p.Jmax, hex.EncodeToString(p.I1)})
}

func (p *Params) UnmarshalJSON(data []byte) error {
	var j paramsJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	m, ok := ParseMask(j.Mask)
	if !ok {
		return fmt.Errorf("unknown obfuscation %q", j.Mask)
	}
	i1, err := hex.DecodeString(j.I1)
	if err != nil {
		return err
	}
	if len(i1) == 0 {
		i1 = nil
	}
	*p = Params{Mask: m, Jc: j.Jc, Jmin: j.Jmin, Jmax: j.Jmax, I1: i1}
	return nil
}

func pick(r [2]int) int {
	if r[0] == r[1] {
		return r[0]
	}
	return r[0] + randInt(r[1]-r[0]+1)
}

func randInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		panic(err)
	}
	return int(v.Int64())
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
