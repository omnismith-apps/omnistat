package upgrade

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/http/httpproxy"
)

// Proxy variables, in the order install captures them (006 FR-013, 007 FR-013).
var proxyNames = [3][2]string{{"HTTPS_PROXY", "https_proxy"}, {"HTTP_PROXY", "http_proxy"}, {"NO_PROXY", "no_proxy"}}

// ErrInsecureRedirect: a redirect from HTTPS down to plain HTTP (FR-010).
var ErrInsecureRedirect = errors.New("refusing a redirect from https to http")

// ProxySettings picks the proxy for the downloads (FR-012): upgrade's own
// environment when it sets any proxy variable, else the settings stored for
// the service. Either spelling counts; the upper-case one wins.
func ProxySettings(getenv func(string) string, stored map[string]string) httpproxy.Config {
	from := func(k string) string { return stored[k] }
	for _, n := range proxyNames {
		if getenv(n[0]) != "" || getenv(n[1]) != "" {
			from = getenv
			break
		}
	}
	pick := func(n [2]string) string {
		if v := strings.TrimSpace(from(n[0])); v != "" {
			return v
		}
		return strings.TrimSpace(from(n[1]))
	}
	return httpproxy.Config{HTTPSProxy: pick(proxyNames[0]), HTTPProxy: pick(proxyNames[1]), NoProxy: pick(proxyNames[2])}
}

// NewClient is the HTTP client of every download (FR-010, FR-012): the
// default transport with the given proxy, at most 10 redirects, never from
// HTTPS down to HTTP. Deadlines and size limits are per request (get).
func NewClient(proxy httpproxy.Config) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	pf := proxy.ProxyFunc()
	t.Proxy = func(r *http.Request) (*url.URL, error) { return pf(r.URL) }
	return &http.Client{
		Transport: t,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if req.URL.Scheme != "https" && via[len(via)-1].URL.Scheme == "https" {
				return ErrInsecureRedirect
			}
			return nil
		},
	}
}

// ErrNotFound: the releases location answered 404.
var ErrNotFound = errors.New("not found")

// get opens rawURL for reading. The caller closes the body, reads at most
// limit bytes of it (limitedBody fails past that), and bounds the request with
// ctx. Only 200 is success.
func get(ctx context.Context, c *http.Client, rawURL string, limit int64) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "omnistat-upgrade")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s: %w (HTTP 404)", rawURL, ErrNotFound)
	case resp.StatusCode != http.StatusOK:
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s: HTTP %s", rawURL, resp.Status)
	case resp.ContentLength > limit:
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s: %d bytes, more than the %d allowed", rawURL, resp.ContentLength, limit)
	}
	return &limitedBody{r: resp.Body, left: limit, url: rawURL}, nil
}

// limitedBody fails a read past its limit instead of truncating silently.
type limitedBody struct {
	r    io.ReadCloser
	left int64
	url  string
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		var one [1]byte
		if n, _ := b.r.Read(one[:]); n > 0 {
			return 0, fmt.Errorf("%s: larger than allowed", b.url)
		}
		return 0, io.EOF
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.r.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *limitedBody) Close() error { return b.r.Close() }
