package upgrade_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/upgrade"
)

// FR-012: upgrade's environment wins when it sets any proxy variable;
// otherwise the service's stored proxy is used. Either spelling counts.
func TestProxySettings(t *testing.T) {
	stored := map[string]string{"HTTPS_PROXY": "http://stored:3128", "NO_PROXY": "internal.example", "OMNISMITH_ACCESS_TOKEN": "x"}
	for _, tc := range []struct {
		name              string
		env               map[string]string
		stored            map[string]string
		https, http, none string
	}{
		{name: "stored", stored: stored, https: "http://stored:3128", none: "internal.example"},
		{name: "environment wins", env: map[string]string{"HTTPS_PROXY": "http://env:3128"}, stored: stored, https: "http://env:3128"},
		{name: "environment NO_PROXY alone wins", env: map[string]string{"no_proxy": "*"}, stored: stored, none: "*"},
		{name: "lower case", env: map[string]string{"https_proxy": "http://lc:3128", "http_proxy": "http://lc:80"}, https: "http://lc:3128", http: "http://lc:80"},
		{name: "upper case first", env: map[string]string{"https_proxy": "http://lc:3128", "HTTPS_PROXY": "http://uc:3128"}, https: "http://uc:3128"},
		{name: "stored lower case", stored: map[string]string{"https_proxy": "http://slc:3128"}, https: "http://slc:3128"},
		{name: "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := upgrade.ProxySettings(func(k string) string { return tc.env[k] }, tc.stored)
			if c.HTTPSProxy != tc.https || c.HTTPProxy != tc.http || c.NoProxy != tc.none {
				t.Fatalf("%+v", c)
			}
		})
	}
}

// FR-012: the client sends remote requests through the proxy, not loopback
// or NO_PROXY hosts.
func TestNewClient_Proxy(t *testing.T) {
	c := upgrade.NewClient(upgrade.ProxySettings(func(k string) string {
		return map[string]string{"HTTPS_PROXY": "http://proxy.example:3128", "NO_PROXY": "mirror.internal"}[k]
	}, nil))
	proxy := c.Transport.(*http.Transport).Proxy
	for target, want := range map[string]string{
		"https://github.com/omnismith-apps/omnistat/releases": "http://proxy.example:3128",
		"https://mirror.internal/omnistat":                    "",
		"http://127.0.0.1:8080/":                              "",
	} {
		u, _ := url.Parse(target)
		p, err := proxy(&http.Request{URL: u})
		got := ""
		if p != nil {
			got = p.String()
		}
		if err != nil || got != want {
			t.Errorf("%s via %q (%v), want %q", target, got, err, want)
		}
	}
}

// FR-010: a redirect from HTTPS to HTTP is refused.
func TestNewClient_NoDowngrade(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) }))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/checksums.txt", http.StatusFound)
	}))
	defer secure.Close()
	c := upgrade.NewClient(upgrade.ProxySettings(func(string) string { return "" }, nil))
	c.Transport.(*http.Transport).TLSClientConfig = secure.Client().Transport.(*http.Transport).TLSClientConfig
	_, err := upgrade.Resolve(context.Background(), c, secure.URL, "", "linux", "amd64")
	if !errors.Is(err, upgrade.ErrInsecureRedirect) {
		t.Fatalf("%v", err)
	}
}
