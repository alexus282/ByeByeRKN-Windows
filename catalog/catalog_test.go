package catalog

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	mrand "math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The Windows app embeds a copy of catalog/warp.json; it must not drift from the repository's.
func TestEmbeddedCopyMatchesRepository(t *testing.T) {
	repo, err := os.ReadFile("../../catalog/warp.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(repo, builtInJSON) {
		t.Fatal("windows/catalog/warp.json differs from catalog/warp.json: copy it over")
	}
	sig, err := os.ReadFile("../../catalog/warp.json.sig")
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(repo, string(sig)) {
		t.Fatal("catalog/warp.json.sig does not verify with the app's key")
	}
}

func TestSignatureCoversEveryByte(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	pub := base64.StdEncoding.EncodeToString(der)
	data := slices.Clone(builtInJSON)
	sum := sha256.Sum256(data)
	raw, _ := ecdsa.SignASN1(rand.Reader, key, sum[:])
	sig := base64.StdEncoding.EncodeToString(raw)
	if !verifyWith(data, sig, pub) {
		t.Fatal("own signature rejected")
	}
	data[len(data)/2]++
	if verifyWith(data, sig, pub) {
		t.Fatal("tampered data accepted")
	}
	if Verify(builtInJSON, sig) {
		t.Fatal("signature by another key accepted")
	}
}

func TestMalformedCatalogsAreRejectedWhole(t *testing.T) {
	for _, bad := range []string{
		`not json`,
		`{"version":2,"ports":[443,70000],"prefixes":["162.159.192."],"quicSni":["ya.ru"]}`,
		`{"version":2,"ports":[443],"prefixes":["162.159.192"],"quicSni":["ya.ru"]}`,
		`{"version":2,"ports":[443],"prefixes":[],"quicSni":["ya.ru"]}`,
		`{"version":2,"ports":[443],"prefixes":["162.159.192."],"quicSni":["not a host"]}`,
		`{"version":2,"ports":[443],"prefixes":["162.159.192."],"wellKnown":["162.159.192.1"],"quicSni":["ya.ru"]}`,
		`{"version":0,"ports":[443],"prefixes":["162.159.192."],"quicSni":["ya.ru"]}`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if _, err := Parse([]byte(`{"version":3,"ports":[443],"prefixes":["162.159.192."],"quicSni":["ya.ru"],"masque":"later"}`)); err != nil {
		t.Errorf("later fields or missing optional ones rejected: %v", err)
	}
}

func TestSampleCoversWholeFamiliesAndSkipsNames(t *testing.T) {
	c := *BuiltIn
	c.WellKnown = []string{"engage.cloudflareclient.com:2408", "162.159.192.1:2408"}
	sample := c.Sample(200, mrand.New(mrand.NewPCG(7, 7)))
	if sample[0] != "162.159.192.1:2408" {
		t.Fatalf("well-known address not first: %v", sample[:3])
	}
	maxHost := 0
	for _, e := range sample {
		if strings.HasPrefix(e, "engage") {
			t.Fatal("a name reached the scan")
		}
		if strings.HasPrefix(e, "[") {
			continue
		}
		host, _ := strconv.Atoi(e[strings.LastIndexByte(HostOf(e), '.')+1 : strings.LastIndexByte(e, ':')])
		if host < 1 || host > 254 {
			t.Fatalf("host out of range: %s", e)
		}
		maxHost = max(maxHost, host)
	}
	if maxHost <= 200 {
		t.Fatalf("only low hosts, highest %d", maxHost)
	}
	tiny := Catalog{Version: 9, Ports: []int{2408}, Prefixes: []string{"162.159.192."}, QuicSNI: []string{"ya.ru"}}
	if got := len(tiny.Sample(300, mrand.New(mrand.NewPCG(1, 1)))); got != 254 {
		t.Fatalf("tiny catalog gave %d endpoints, want every one of 254", got)
	}
}

func TestEndpointParts(t *testing.T) {
	if FamilyOf("162.159.192.7:2408") != "162.159.192." || FamilyOf("[2606:4700:d0::a29f:c001]:2408") != V6Family {
		t.Fatal("wrong family")
	}
	if HostOf("[2606:4700:d0::a29f:c001]:2408") != "2606:4700:d0::a29f:c001" || PortOf("[2606:4700:d0::a29f:c001]:2408") != 2408 {
		t.Fatal("wrong host or port")
	}
	if !IsLiteral("162.159.192.1") || IsLiteral("engage.cloudflareclient.com") || IsLiteral("") {
		t.Fatal("wrong literal check")
	}
	if slices.Contains(BuiltIn.Prefixes, "162.159.193.") {
		t.Fatal("the Zero Trust range is back")
	}
}
