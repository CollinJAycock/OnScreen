package v1

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDescribeUserAgent(t *testing.T) {
	cases := []struct {
		ua, name, platform string
	}{
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36", "Chrome on Windows", "web"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 Edg/128.0.0.0", "Edge on Windows", "web"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15", "Safari on macOS", "web"},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0", "Firefox on Linux", "web"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", "Safari on iOS", "ios"},
		{"Mozilla/5.0 (Linux; Android 14; SM-S711U) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Mobile Safari/537.36", "Chrome on Android", "web"},
		{"okhttp/4.12.0", "Android app", "android"},
		{"Roku/DVP-14.0 (14.0.0.4201-AE)", "Roku", "roku"},
		{"Mozilla/5.0 (SMART-TV; LINUX; Tizen 6.5) AppleWebKit/537.36 (KHTML, like Gecko) 85.0.4183.93/6.5 TV Safari/537.36", "Samsung TV", "tizen"},
		{"Mozilla/5.0 (Web0S; Linux/SmartTV) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/79.0.3945.79 Safari/537.36 WebAppManager", "LG TV", "webos"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64; Xbox; Xbox One) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 Edg/128.0.0.0", "Xbox", "xbox"},
		{"curl/8.9.1", "curl", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		name, platform := describeUserAgent(c.ua)
		if name != c.name || platform != c.platform {
			t.Errorf("describeUserAgent(%q) = (%q, %q), want (%q, %q)", c.ua, name, platform, c.name, c.platform)
		}
	}
}

// The recorded IP is the trusted RemoteAddr (what TrustedRealIP resolved),
// never a raw X-Forwarded-For the client sent; X-Device-Name overrides the
// UA-derived label but is sanitised and bounded.
func TestDescribeSessionClient(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	r.RemoteAddr = "198.51.100.4:51234"
	r.Header.Set("X-Forwarded-For", "10.0.0.9")
	r.Header.Set("User-Agent", "okhttp/4.12.0")
	r.Header.Set("X-Device-Name", "Living room\x00 TV"+strings.Repeat("x", 100))
	c := describeSessionClient(r)
	if c.IP == nil || c.IP.String() != "198.51.100.4" {
		t.Errorf("IP = %v, want the trusted RemoteAddr 198.51.100.4", c.IP)
	}
	if c.Platform != "android" {
		t.Errorf("platform = %q", c.Platform)
	}
	if !strings.HasPrefix(c.ClientName, "Living room TV") || len([]rune(c.ClientName)) != maxClientLabel {
		t.Errorf("client name = %q", c.ClientName)
	}
}
