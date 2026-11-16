package rules_test

import (
	"context"
	"crypto/x509"
	"testing"
	"time"

	"whynet/internal/probe"
	"whynet/rules"
)

// The app check in the lab: recorded servers (what the Windows table would give) go through the
// real naming and checking against local servers, then the app rules.
func TestAppLab(t *testing.T) {
	good := serve(t, 200, "<title>ok</title>")
	cert := good.TLS.Certificates[0] // valid for example.com
	dpi := dpiServer(t, cert, "example.com", false)
	balancer := dpiServer(t, cert, "example.com", true) // closes every other name, the empty one too
	pool := x509.NewCertPool()
	pool.AddCert(good.Certificate())
	l := &lab{pool: pool, routes: map[string]string{
		"203.0.113.1:443":  good.Listener.Addr().String(),
		"203.0.113.2:443":  dpi,
		"203.0.113.3:5222": balancer, // a program's own port
		"198.18.0.7:443":   dpi,      // behind a fake-ip router
		// 203.0.113.9:2053 and the ad server are black holes
	}}
	env := l.env(nil, nil, []string{"203.0.113.2"}, nil)
	rs, err := rules.LoadApp()
	if err != nil {
		t.Fatal(err)
	}
	srv := func(ip string, port uint16, conns, stuck, cut int) *probe.AppServer {
		return &probe.AppServer{IP: ip, Port: port, FakeIP: ip[:7] == "198.18.", Conns: conns, Stuck: stuck, Cut: cut, Open: conns - stuck - cut}
	}
	cases := []struct {
		name    string
		servers []*probe.AppServer
		cache   map[string]string
		want    string
	}{
		{"no connections", nil, nil, "app_no_traffic"},
		{"all fine", []*probe.AppServer{srv("203.0.113.1", 443, 3, 0, 0)}, nil, "app_ok"},
		{"cut by name, named by its certificate", []*probe.AppServer{srv("203.0.113.1", 443, 2, 0, 0), srv("203.0.113.2", 443, 4, 0, 4)}, nil, "app_blocked_name"},
		{"cut, the name can't be learned", []*probe.AppServer{srv("203.0.113.3", 5222, 3, 0, 3)}, nil, "app_blocked_maybe"},
		{"silent on its port", []*probe.AppServer{srv("203.0.113.9", 2053, 2, 2, 0)}, nil, "app_unreachable"},
		{"cut behind a fake-ip router", []*probe.AppServer{srv("198.18.0.7", 443, 3, 0, 3)}, nil, "app_router_path"},
		{"the program saw cuts, the check passed", []*probe.AppServer{srv("203.0.113.1", 443, 3, 0, 2)}, nil, "app_flaky"},
		{"a silent ad server is not blamed", []*probe.AppServer{srv("203.0.113.1", 443, 3, 0, 0), srv("203.0.113.8", 443, 1, 1, 0)},
			map[string]string{"203.0.113.8": "stats.doubleclick.net"}, "app_ok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := probe.CheckApp(context.Background(), env, "Test.exe", c.servers, 20*time.Second, c.cache, nil)
			if got := rules.Match(rs, res.Facts()); got.ID != c.want {
				t.Errorf("verdict %s, want %s; facts %v", got.ID, c.want, res.Facts())
				for _, s := range res.Servers {
					t.Logf("  %+v", *s)
				}
			}
			if len(res.Steps) != len(probe.AppStepIDs) {
				t.Errorf("%d steps, want %d", len(res.Steps), len(probe.AppStepIDs))
			}
		})
	}
}
