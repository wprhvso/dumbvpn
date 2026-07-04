package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/go-gost/core/logger"
	"github.com/go-gost/core/service"
	"github.com/go-gost/x/config"
	chain_parser "github.com/go-gost/x/config/parsing/chain"
	service_parser "github.com/go-gost/x/config/parsing/service"
	xlogger "github.com/go-gost/x/logger"
	"github.com/go-gost/x/registry"

	_ "github.com/go-gost/x/connector/relay"
	_ "github.com/go-gost/x/dialer/http2/h2"
	_ "github.com/go-gost/x/handler/tungo"
	_ "github.com/go-gost/x/listener/tungo"

	_ "github.com/go-gost/x/handler/auto"
	_ "github.com/go-gost/x/handler/http"
	_ "github.com/go-gost/x/handler/socks/v4"
	_ "github.com/go-gost/x/handler/socks/v5"
	_ "github.com/go-gost/x/listener/tcp"

	cconnector "github.com/go-gost/core/connector"
	cmetadata "github.com/go-gost/core/metadata"
	cresolver "github.com/go-gost/core/resolver"
	resolver_parser "github.com/go-gost/x/config/parsing/resolver"
	"github.com/gobwas/glob"
	"github.com/miekg/dns"

	_ "github.com/go-gost/x/connector/direct"
	_ "github.com/go-gost/x/dialer/udp"
)

var (
	activeServices []service.Service
	localDNSServer *dns.Server
	serviceMux     sync.Mutex
	lifecycleMux   sync.Mutex
	dnsCache       sync.Map
	engineCancel   context.CancelFunc
	engineWG       sync.WaitGroup

	sendLog      = func(format string, args ...interface{}) { fmt.Printf(format+"\n", args...) }
	platformInit = func() {}

	directDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}
)

var bypassPorts = map[string]bool{
	"22":   true,
	"123":  true,
	"4460": true,
}

type CustomRelayConnector struct {
	originalConnector cconnector.Connector
	bypassIPs         *sync.Map
}

func (c *CustomRelayConnector) Init(md cmetadata.Metadata) error {
	return c.originalConnector.Init(md)
}

func (c *CustomRelayConnector) Connect(ctx context.Context, conn net.Conn, network, address string, opts ...cconnector.ConnectOption) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	host = strings.Trim(host, "[]")

	if host == "10.0.0.1" && port == "53" {
		sendLog("[DNS-Redirect] Intercepting DNS query to %s. Redirecting directly to 127.0.0.1:10533...", address)
		if conn != nil {
			conn.Close()
		}
		return net.Dial("udp", "127.0.0.1:10533")
	}

	if bypassPorts[port] {
		sendLog("[Bypass-Port] Connecting DIRECTLY to %s over %s (Bypassing port %s)...", address, network, port)
		if conn != nil {
			conn.Close()
		}
		return directDialContext(ctx, network, address)
	}

	parsedIP := net.ParseIP(host)
	if parsedIP != nil {
		if parsedIP.IsLoopback() || parsedIP.IsPrivate() || parsedIP.IsLinkLocalUnicast() || parsedIP.IsLinkLocalMulticast() {
			sendLog("[Bypass-Local] Connecting DIRECTLY to local/private destination %s over %s...", address, network)
			if conn != nil {
				conn.Close()
			}
			return directDialContext(ctx, network, address)
		}
	}

	sendLog("[Bypass-Check] Checking routing for IP: %s", host)
	if _, ok := c.bypassIPs.Load(host); ok {
		sendLog("[Bypass-Direct] MATCH! Connecting DIRECTLY to %s over %s (Bypassing proxy)...", address, network)
		if conn != nil {
			conn.Close()
		}
		return directDialContext(ctx, network, address)
	}

	return c.originalConnector.Connect(ctx, conn, network, address, opts...)
}

func startLocalDNSServer(ctx context.Context, bypassIPs *sync.Map, proxyResolver cresolver.Resolver, patterns []string) {
	globs := make([]glob.Glob, 0, len(patterns))
	for _, p := range patterns {
		g, err := glob.Compile(strings.ToLower(p))
		if err != nil {
			sendLog("Error compiling glob pattern %s: %v", p, err)
			continue
		}
		globs = append(globs, g)
	}

	directClient := &dns.Client{Net: "udp"}

	matchDomain := func(host string) bool {
		host = strings.TrimSuffix(host, ".")
		host = strings.ToLower(host)
		for _, g := range globs {
			if g.Match(host) {
				return true
			}
		}
		return false
	}

	dnsHandler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		if len(r.Question) == 0 {
			dns.HandleFailed(w, r)
			return
		}

		q := r.Question[0]
		host := q.Name
		cleanHost := strings.TrimSuffix(host, ".")
		cleanHost = strings.ToLower(cleanHost)

		cacheKey := fmt.Sprintf("%s:%d", cleanHost, q.Qtype)

		if val, ok := dnsCache.Load(cacheKey); ok {
			if cachedRRs, ok := val.([]dns.RR); ok {
				resp := &dns.Msg{}
				resp.SetReply(r)
				resp.Answer = cachedRRs
				w.WriteMsg(resp)
				return
			}
		}

		var resp *dns.Msg
		var err error

		if matchDomain(host) {
			sendLog("Bypass match: %s. Querying directly...", host)
			resp, _, err = directClient.Exchange(r, "1.1.1.1:53")
			if err == nil && resp != nil {
				if resp.Rcode == dns.RcodeSuccess {
					dnsCache.Store(cacheKey, resp.Answer)
				}
				for _, answer := range resp.Answer {
					if aRecord, ok := answer.(*dns.A); ok {
						bypassIPs.Store(aRecord.A.String(), true)
						sendLog("Learned bypass IPv4: %s -> %s", host, aRecord.A.String())
					}
					if aaaaRecord, ok := answer.(*dns.AAAA); ok {
						bypassIPs.Store(aaaaRecord.AAAA.String(), true)
						sendLog("Learned bypass IPv6: %s -> %s", host, aaaaRecord.AAAA.String())
					}
				}
			}
		} else {
			sendLog("Tunnel match: %s. Resolving over proxy...", host)
			resp = &dns.Msg{}
			resp.SetReply(r)

			if q.Qtype == dns.TypeA {
				ips, err := proxyResolver.Resolve(ctx, "ip4", cleanHost)
				if err == nil {
					var rrs []dns.RR
					for _, ip := range ips {
						rr := &dns.A{
							Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
							A:   ip,
						}
						resp.Answer = append(resp.Answer, rr)
						rrs = append(rrs, rr)
					}
					if len(rrs) > 0 {
						dnsCache.Store(cacheKey, rrs)
					}
				}
			} else if q.Qtype == dns.TypeAAAA {
				ips, err := proxyResolver.Resolve(ctx, "ip6", cleanHost)
				if err == nil {
					var rrs []dns.RR
					for _, ip := range ips {
						rr := &dns.AAAA{
							Hdr:  dns.RR_Header{Name: q.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60},
							AAAA: ip,
						}
						resp.Answer = append(resp.Answer, rr)
						rrs = append(rrs, rr)
					}
					if len(rrs) > 0 {
						dnsCache.Store(cacheKey, rrs)
					}
				}
			} else {
				resp, _, err = directClient.Exchange(r, "8.8.8.8:53")
				if err == nil && resp != nil {
					if resp.Rcode == dns.RcodeSuccess {
						dnsCache.Store(cacheKey, resp.Answer)
					}
				}
			}
		}

		if err != nil || resp == nil {
			dns.HandleFailed(w, r)
			return
		}
		w.WriteMsg(resp)
	})

	server := &dns.Server{
		Addr:    "0.0.0.0:10533",
		Net:     "udp",
		Handler: dnsHandler,
	}

	serviceMux.Lock()
	localDNSServer = server
	serviceMux.Unlock()

	engineWG.Add(1)
	go func() {
		defer engineWG.Done()
		<-ctx.Done()
		serviceMux.Lock()
		if localDNSServer == server {
			server.Shutdown()
			localDNSServer = nil
		}
		serviceMux.Unlock()
		sendLog("Local DNS Server successfully shut down.")
	}()

	sendLog("Starting local DNS Server on 0.0.0.0:10533...")
	if err := server.ListenAndServe(); err != nil {
		sendLog("Local DNS Server ListenAndServe finished: %v", err)
	}
}

func stopVpnEngineInternal() {
	lifecycleMux.Lock()
	defer lifecycleMux.Unlock()

	serviceMux.Lock()
	cancel := engineCancel
	engineCancel = nil
	serviceMux.Unlock()

	if cancel != nil {
		sendLog("Canceling active GoCore engine context...")
		cancel()
	}

	sendLog("Waiting for all active GoCore goroutines to terminate...")
	engineWG.Wait()
	sendLog("All previous GoCore goroutines successfully terminated.")

	dnsCache.Range(func(key, value any) bool {
		dnsCache.Delete(key)
		return true
	})
}

func startVpnEngine(fd int) {
	lifecycleMux.Lock()
	defer lifecycleMux.Unlock()

	serviceMux.Lock()
	cancel := engineCancel
	engineCancel = nil
	serviceMux.Unlock()

	if cancel != nil {
		sendLog("Canceling active GoCore engine context...")
		cancel()
	}

	sendLog("Waiting for all active GoCore goroutines to terminate...")
	engineWG.Wait()
	sendLog("All previous GoCore goroutines successfully terminated.")

	dnsCache.Range(func(key, value any) bool {
		dnsCache.Delete(key)
		return true
	})

	ctx, cancel := context.WithCancel(context.Background())
	serviceMux.Lock()
	engineCancel = cancel
	serviceMux.Unlock()

	runEngineInstance(ctx, fd)
}

func runEngineInstance(ctx context.Context, fd int) {
	platformInit()
	logger.SetDefault(xlogger.NewLogger())

	bypassIPs := &sync.Map{}

	originalRelayFactory := registry.ConnectorRegistry().Get("relay")
	registry.ConnectorRegistry().Unregister("relay")
	registry.ConnectorRegistry().Register("relay", func(opts ...cconnector.Option) cconnector.Connector {
		orig := originalRelayFactory(opts...)
		return &CustomRelayConnector{
			originalConnector: orig,
			bypassIPs:         bypassIPs,
		}
	})

	tunMetadata := map[string]any{
		"net": "10.0.0.2/24",
		"mtu": 1500,
	}
	if fd > 0 {
		tunMetadata["fd"] = fd
	}

	cfg := &config.Config{
		Chains: []*config.ChainConfig{
			{
				Name: "chain-0",
				Hops: []*config.HopConfig{
					{
						Name: "hop-0",
						Nodes: []*config.NodeConfig{
							{
								Name: "node-0",
								Addr: "79.137.207.89:8443",
								Connector: &config.ConnectorConfig{
									Type: "relay",
									Auth: &config.AuthConfig{
										Username: "user",
										Password: "tnuymarralstgvlxsu",
									},
								},
								Dialer: &config.DialerConfig{
									Type: "h2",
									TLS: &config.TLSConfig{
										ServerName: "79.137.207.89",
									},
									Metadata: map[string]any{
										"path": "/api/v1/relay",
									},
								},
							},
						},
					},
				},
			},
		},
		Services: []*config.ServiceConfig{
			{
				Name: "service-0",
				Addr: "tungo",
				Listener: &config.ListenerConfig{
					Type:     "tungo",
					Metadata: tunMetadata,
				},
				Handler: &config.HandlerConfig{
					Type:  "tungo",
					Chain: "chain-0",
				},
			},
			{
				Name: "service-socks",
				Addr: ":1080",
				Listener: &config.ListenerConfig{
					Type: "tcp",
				},
				Handler: &config.HandlerConfig{
					Type:  "auto",
					Chain: "chain-0",
				},
			},
		},
	}

	ch, err := chain_parser.ParseChain(cfg.Chains[0], logger.Default())
	if err != nil {
		sendLog("Failed to parse chain: %v", err)
		return
	}
	registry.ChainRegistry().Register("chain-0", ch)

	dnsOverProxyCfg := &config.ResolverConfig{
		Name: "proxy-dns",
		Nameservers: []*config.NameserverConfig{
			{
				Addr:  "8.8.8.8:53",
				Chain: "chain-0",
			},
		},
	}
	proxyResolver, err := resolver_parser.ParseResolver(dnsOverProxyCfg)
	if err != nil {
		sendLog("Failed to parse proxy DNS resolver: %v", err)
		return
	}

	patterns := []string{
		"*.ru",
		"*.internal",
		"*.local",
		"*.ru-central1.internal",
		"*.ru-central2.internal",
		"*.su",
		"*.xn--p1ai",
		"*.xn--p1acf",
		"*.xn--80adxhks",
		"*.xn--80aswg",
		"*.xn--80asehdb",
		"*.xn--d1acj3b",
		"*.moscow",
		"*.tatar",
		"*.by",
		"*.xn--90ais",
		"*.kz",
		"*.xn--80ao21a",
		"*.uz",
		"*.am",
		"*.kg",
		"*.yandex",
		"*.sber",
		"*.yandex.net",
		"*.yandex.com",
		"*.yandex.by",
		"*.yandex.kz",
		"*.yandex.uz",
		"*.yandex.az",
		"*.yandex.co.il",
		"*.yastatic.net",
		"*.yametrika.com",
		"*.yadi.sk",
		"*.ya.cc",
		"*.yandex-team.com",
		"*.vk.com",
		"*.vk.me",
		"*.vkuserimages.com",
		"*.vkuservideo.net",
		"*.vk-cdn.net",
		"*.vk-portal.net",
		"*.mycdn.me",
		"*.ok.me",
		"*.odnoklassniki.com",
		"*.ozoncdn.com",
		"*.ozon.travel",
		"*.wbstatic.net",
		"*.avito.st",
		"*.habr.com",
		"*.habrastorage.org",
		"*.selectel.com",
		"*.timeweb.com",
		"*.edge-center.video",
		"*.gcdn.co",
		"*.gcore.com",
		"*.gcorelabs.com",
		"*.ispsystem.com",
		"*.sberbank.com",
		"*.sbercloud.com",
		"*.sberdevices.com",
		"*.tinkoffgroup.com",
		"*.qiwi.com",
		"*.moex.com",
		"*.webmoney.com",
		"*.okko.tv",
		"*.okko.sport",
		"*.premier.one",
		"*.more.tv",
		"*.zvuk.com",
		"*.yaplakal.com",
		"*.kaspersky.com",
		"*.kaspersky-labs.com",
		"*.drweb.com",
		"*.2gis.com",
		"*.doublegis.com",
		"*.bybit.com",
	}

	engineWG.Add(1)
	go func() {
		defer engineWG.Done()
		startLocalDNSServer(ctx, bypassIPs, proxyResolver, patterns)
	}()

	var parsedServices []service.Service
	for _, svcCfg := range cfg.Services {
		srv, err := service_parser.ParseService(svcCfg)
		if err != nil {
			sendLog("Failed to parse service %s: %v", svcCfg.Name, err)
			continue
		}
		parsedServices = append(parsedServices, srv)
	}

	serviceMux.Lock()
	activeServices = parsedServices
	serviceMux.Unlock()

	engineWG.Add(1)
	go func() {
		defer engineWG.Done()
		<-ctx.Done()
		serviceMux.Lock()
		for _, srv := range activeServices {
			if srv != nil {
				srv.Close()
			}
		}
		activeServices = nil
		serviceMux.Unlock()
		sendLog("All GOST services successfully closed.")
	}()

	for _, srv := range parsedServices {
		isTun := strings.Contains(strings.ToLower(srv.Addr().String()), "tungo") || strings.Contains(strings.ToLower(srv.Addr().Network()), "tungo")
		if isTun {
			go func(s service.Service) {
				if err := s.Serve(); err != nil {
					sendLog("GOST service %s stopped or exited: %v", s.Addr().String(), err)
				}
			}(srv)
		} else {
			engineWG.Add(1)
			go func(s service.Service) {
				defer engineWG.Done()
				if err := s.Serve(); err != nil {
					sendLog("GOST service %s stopped or exited: %v", s.Addr().String(), err)
				}
			}(srv)
		}
	}

	sendLog("TUNGO and SOCKS5 running directly via FD %d", fd)
}
