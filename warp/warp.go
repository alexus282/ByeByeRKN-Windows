// Package warp registers a device with Cloudflare's (undocumented) WARP client API, the same
// calls the official client makes: POST reg and PATCH reg/{id}. The private key is generated
// locally and never leaves the device.
package warp

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	apiVersion     = "v0i1909051800"
	masqueVersion  = "v0a4471"
	defaultTimeout = 10 * time.Second
)

// DefaultHosts are the API hosts in the order the app tries them. Russian ISPs drop the TLS
// handshake to api.cloudflareclient.com by SNI, while zero-trust-client.cloudflareclient.com
// serves the same API and passes.
var DefaultHosts = []string{"zero-trust-client.cloudflareclient.com", "api.cloudflareclient.com"}

// WarpAccount is a device enrolled for AmneziaWG.
type WarpAccount struct {
	ID            string `json:"id"`
	Token         string `json:"token"`
	PrivateKey    string `json:"privateKey"`
	PeerPublicKey string `json:"peerPublicKey"`
	AddressV4     string `json:"addressV4"`
	AddressV6     string `json:"addressV6"`
}

// MasqueAccount is a device enrolled for MASQUE. Enrolling a MASQUE key replaces a device's
// WireGuard key, so the AmneziaWG account can't double as this one.
type MasqueAccount struct {
	ID                string `json:"id"`
	Token             string `json:"token"`
	PrivateKey        string `json:"privateKey"`
	EndpointPublicKey string `json:"endpointPublicKey"`
	EndpointV4        string `json:"endpointV4"`
	EndpointV6        string `json:"endpointV6"`
	AddressV4         string `json:"addressV4"`
	AddressV6         string `json:"addressV6"`
}

// WarpPlus is what binding a WARP+ key gave.
type WarpPlus struct {
	AccountType string `json:"account_type"`
	WarpPlus    bool   `json:"warp_plus"`
}

// Error carries Cloudflare's own answer. A non-nil HTTPCode means the server responded, so
// another route would not change the outcome.
type Error struct {
	Message    string
	HTTPCode   *int
	APIMessage string
}

func (e *Error) Error() string {
	if e.APIMessage != "" {
		return fmt.Sprintf("warp: HTTP %d: %s", *e.HTTPCode, e.APIMessage)
	}
	return "warp: " + e.Message
}

// Client talks to the API, trying every host with a direct TLS handshake first and a split
// ClientHello as a last resort (see [SplitClientHello]).
type Client struct {
	Hosts  []string
	Client *http.Client
	// Split is the client used when the direct one failed for every host.
	Split *http.Client
	// OnRetry is called before each attempt after the first, so a UI can show progress.
	OnRetry func(host, mode string)
}

func NewClient() *Client {
	return &Client{
		Hosts:  DefaultHosts,
		Client: &http.Client{Timeout: defaultTimeout},
		Split: &http.Client{
			Timeout:   15 * time.Second,
			Transport: &http.Transport{DialContext: SplitDialer},
		},
	}
}

// Register creates an AmneziaWG device the way the current official client does.
func (c *Client) Register() (*WarpAccount, error) {
	keyPair, err := GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"install_id": "",
		"tos":        time.Now().UTC().Format(time.RFC3339),
		"key":        keyPair.Public,
		"fcm_token":  "",
		"type":       "ios",
		"locale":     "en_US",
	}
	registration, err := c.call(apiVersion, http.MethodPost, "reg", body, "", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := unwrap(registration, &result); err != nil {
		return nil, err
	}

	config, err := c.call(apiVersion, http.MethodPatch, "reg/"+result.ID, map[string]any{"warp_enabled": true}, result.Token, nil)
	if err != nil {
		return nil, err
	}
	var patched struct {
		Config struct {
			Peers []struct {
				PublicKey string `json:"public_key"`
			} `json:"peers"`
			Interface struct {
				Addresses struct {
					V4 string `json:"v4"`
					V6 string `json:"v6"`
				} `json:"addresses"`
			} `json:"interface"`
		} `json:"config"`
	}
	if err := unwrap(config, &patched); err != nil {
		return nil, err
	}
	if len(patched.Config.Peers) == 0 {
		return nil, errors.New("warp: no peer in the config")
	}
	return &WarpAccount{
		ID:            result.ID,
		Token:         result.Token,
		PrivateKey:    keyPair.Private,
		PeerPublicKey: patched.Config.Peers[0].PublicKey,
		AddressV4:     patched.Config.Interface.Addresses.V4,
		AddressV6:     patched.Config.Interface.Addresses.V6,
	}, nil
}

// MasqueHeaders are the headers the official Android client sends to the MASQUE API.
func MasqueHeaders() map[string]string {
	return map[string]string{
		"User-Agent":        "WARP for Android",
		"CF-Client-Version": "a-6.35-4471",
	}
}

// RegisterMasque enrolls a P-256 key, as the official client does when it needs MASQUE.
func (c *Client) RegisterMasque() (*MasqueAccount, error) {
	keyPair, err := GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	serial := make([]byte, 8)
	rand.Read(serial)
	body := map[string]any{
		"key":           keyPair.Public,
		"install_id":    "",
		"fcm_token":     "",
		"tos":           time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		"model":         "PC",
		"serial_number": fmt.Sprintf("%x", serial),
		"os_version":    "",
		"key_type":      "curve25519",
		"tunnel_type":   "wireguard",
		"locale":        "en_US",
	}
	registration, err := c.call(masqueVersion, http.MethodPost, "reg", body, "", MasqueHeaders())
	if err != nil {
		return nil, err
	}
	var result struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := unwrap(registration, &result); err != nil {
		return nil, err
	}

	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKIXPublicKey(&ec.PublicKey)
	if err != nil {
		return nil, err
	}
	enrolled, err := c.call(masqueVersion, http.MethodPatch, "reg/"+result.ID, map[string]any{
		"key":         base64.StdEncoding.EncodeToString(der),
		"key_type":    "secp256r1",
		"tunnel_type": "masque",
	}, result.Token, MasqueHeaders())
	if err != nil {
		return nil, err
	}
	var patched struct {
		Config struct {
			Peers []struct {
				PublicKey string `json:"public_key"`
				Endpoint  struct {
					V4 string `json:"v4"`
					V6 string `json:"v6"`
				} `json:"endpoint"`
			} `json:"peers"`
			Interface struct {
				Addresses struct {
					V4 string `json:"v4"`
					V6 string `json:"v6"`
				} `json:"addresses"`
			} `json:"interface"`
		} `json:"config"`
	}
	if err := unwrap(enrolled, &patched); err != nil {
		return nil, err
	}
	if len(patched.Config.Peers) == 0 {
		return nil, errors.New("warp: no peer in the MASQUE config")
	}
	peer := patched.Config.Peers[0]
	privateDER, err := x509.MarshalPKCS8PrivateKey(ec)
	if err != nil {
		return nil, err
	}
	return &MasqueAccount{
		ID:                result.ID,
		Token:             result.Token,
		PrivateKey:        base64.StdEncoding.EncodeToString(privateDER),
		EndpointPublicKey: peer.PublicKey,
		EndpointV4:        hostOf(peer.Endpoint.V4),
		EndpointV6:        hostOf(peer.Endpoint.V6),
		AddressV4:         patched.Config.Interface.Addresses.V4,
		AddressV6:         patched.Config.Interface.Addresses.V6,
	}, nil
}

// ApplyLicense binds a WARP+ key to an AmneziaWG device, as the official client's "change key"
// does.
func (c *Client) ApplyLicense(account *WarpAccount, license string) (*WarpPlus, error) {
	return c.applyLicense(apiVersion, "", account.ID, account.Token, license, nil)
}

// ApplyLicense is the same for the MASQUE device: one key covers several devices.
func (c *Client) ApplyLicenseMasque(account *MasqueAccount, license string) (*WarpPlus, error) {
	return c.applyLicense(masqueVersion, "", account.ID, account.Token, license, MasqueHeaders())
}

func (c *Client) applyLicense(version, _, id, token, license string, headers map[string]string) (*WarpPlus, error) {
	reply, err := c.call(version, http.MethodPut, "reg/"+id+"/account", map[string]any{"license": license}, token, headers)
	if err != nil {
		return nil, err
	}
	var plus WarpPlus
	if err := unwrap(reply, &plus); err != nil {
		return nil, err
	}
	return &plus, nil
}

// hostOf is "162.159.198.1:0" or "[2606:4700:103::]:0" without the port and brackets.
func hostOf(endpoint string) string {
	host := endpoint[:strings.LastIndexByte(endpoint, ':')]
	return strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
}

var licensePattern = regexp.MustCompile(`^[A-Za-z0-9]{8}-[A-Za-z0-9]{8}-[A-Za-z0-9]{8}$`)

// IsLicense reports whether a WARP+ key looks like xxxxxxxx-xxxxxxxx-xxxxxxxx.
func IsLicense(key string) bool { return licensePattern.MatchString(key) }

// call performs one request, trying every host directly and then with a split ClientHello.
// A server answer (any HTTP status) stops the loop: another route would not change it.
func (c *Client) call(version, method, path string, body any, token string, headers map[string]string) (json.RawMessage, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	hosts := c.Hosts
	if len(hosts) == 0 {
		hosts = DefaultHosts
	}
	type attempt struct {
		host  string
		split bool
	}
	var attempts []attempt
	for _, host := range hosts {
		attempts = append(attempts, attempt{host: host})
	}
	for _, host := range hosts {
		attempts = append(attempts, attempt{host: host, split: true})
	}

	var last *Error
	for i, a := range attempts {
		if i > 0 && c.OnRetry != nil {
			mode := "direct"
			if a.split {
				mode = "split"
			}
			c.OnRetry(a.host, mode)
		}
		client := c.Client
		if a.split {
			client = c.Split
		}
		if client == nil {
			client = http.DefaultClient
		}
		url := "https://" + a.host + "/" + version + "/" + path
		result, err := do(client, method, url, payload, token, headers)
		if err == nil {
			return result, nil
		}
		var apiErr *Error
		if errors.As(err, &apiErr) && apiErr.HTTPCode != nil {
			return nil, apiErr
		}
		last = apiErr
		if last == nil {
			last = &Error{Message: err.Error()}
		}
	}
	if last == nil {
		last = &Error{Message: "no API hosts configured"}
	}
	return nil, last
}

func do(client *http.Client, method, url string, payload []byte, token string, headers map[string]string) (json.RawMessage, error) {
	request, err := http.NewRequest(method, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "CloudflareWarp/1.0")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, &Error{Message: err.Error()}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, &Error{Message: err.Error()}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code := response.StatusCode
		apiErr := &Error{HTTPCode: &code, Message: fmt.Sprintf("HTTP %d: %s", code, truncate(string(data), 200))}
		var problem struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if json.Unmarshal(data, &problem) == nil && len(problem.Errors) > 0 {
			apiErr.APIMessage = problem.Errors[0].Message
		}
		return nil, apiErr
	}
	return json.RawMessage(data), nil
}

// unwrap reads the object itself or the "result" wrapper the old API used.
func unwrap(raw json.RawMessage, out any) error {
	var wrapper struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil && len(wrapper.Result) > 0 {
		raw = wrapper.Result
	}
	return json.Unmarshal(raw, out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
