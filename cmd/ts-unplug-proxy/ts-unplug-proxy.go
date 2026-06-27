package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/net/dns/dnsmessage"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/net/proxymux"
	"tailscale.com/net/socks5"
	"tailscale.com/tsnet"
)

const tsnetHostname = "tsunplug-proxy"

var (
	flagDir          = flag.String("dir", "", "tsnet server directory")
	flagVerboseProxy = flag.Bool("v", false, "log proxy connections")
	flagVerboseTSNet = flag.Bool("vv", false, "log proxy connections and tsnet debug info")
	flagSOCKS5Addr   = flag.String("socks5", "", "SOCKS5 proxy listen address")
	flagHTTPAddr     = flag.String("http", "", "HTTP proxy listen address")
	flagNoExitNode   = flag.Bool("disable-exit-node", false, "disable automatic tailnet exit node use")
)

type serveResult struct {
	name string
	err  error
}

type tailnetDialer struct {
	ts *tsnet.Server
	lc *local.Client
}

type loggingListener struct {
	net.Listener
	proxy string
}

func (ln loggingListener) Accept() (net.Conn, error) {
	conn, err := ln.Listener.Accept()
	if err != nil {
		return nil, err
	}
	slog.Debug("proxy connection accepted",
		slog.String("proxy", ln.proxy),
		slog.String("local", conn.LocalAddr().String()),
		slog.String("remote", conn.RemoteAddr().String()),
	)
	return conn, nil
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s -dir <state-dir> (-socks5 <listen:port> | -http <listen:port>) [options]\n\n", os.Args[0])
		fmt.Fprintln(flag.CommandLine.Output(), "Starts local SOCKS5 and/or HTTP proxies that dial destinations through a tsnet.Server.")
		fmt.Fprintln(flag.CommandLine.Output())
		flag.PrintDefaults()
	}

	flag.Parse()
	if *flagDir == "" {
		slog.Error("dir is required")
		os.Exit(1)
	}
	if *flagSOCKS5Addr == "" && *flagHTTPAddr == "" {
		slog.Error("at least one of -socks5 or -http is required")
		os.Exit(1)
	}
	if flag.NArg() != 0 {
		slog.Error("positional remote addresses are not supported; proxy clients provide destinations")
		os.Exit(1)
	}
	verboseProxy := *flagVerboseProxy || *flagVerboseTSNet
	if verboseProxy {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ts := &tsnet.Server{
		Hostname: tsnetHostname,
		Dir:      *flagDir,
	}
	if *flagVerboseTSNet {
		ts.Logf = func(format string, args ...any) {
			slog.Debug(fmt.Sprintf(format, args...))
		}
	}

	st, err := ts.Up(ctx)
	if err != nil {
		slog.Error("error starting tsnet server", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() {
		if err := ts.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			slog.Warn("error closing tsnet server", slog.Any("error", err))
		}
	}()

	slog.Info("tsnet server started", slog.String("status", st.BackendState))

	lc, err := ts.LocalClient()
	if err != nil {
		slog.Error("failed to get tsnet local client", slog.Any("error", err))
		os.Exit(1)
	}
	if !*flagNoExitNode {
		if err := useExitNodeIfAvailable(ctx, lc); err != nil {
			slog.Warn("failed to configure tailnet exit node", slog.Any("error", err))
		}
	}
	dialer := (&tailnetDialer{ts: ts, lc: lc}).Dial

	serveErr := make(chan serveResult, 2)
	var closers []io.Closer

	if *flagSOCKS5Addr != "" && *flagSOCKS5Addr == *flagHTTPAddr {
		listener, err := net.Listen("tcp", *flagSOCKS5Addr)
		if err != nil {
			slog.Error("failed to listen", slog.String("addr", *flagSOCKS5Addr), slog.Any("error", err))
			os.Exit(1)
		}
		closers = append(closers, listener)

		socksListener, httpListener := proxymux.SplitSOCKSAndHTTP(listener)
		if verboseProxy {
			socksListener = loggingListener{Listener: socksListener, proxy: "socks5"}
			httpListener = loggingListener{Listener: httpListener, proxy: "http"}
		}
		startSOCKS5Proxy(socksListener, dialer, serveErr)
		startHTTPProxy(httpListener, dialer, serveErr)
		slog.Info("SOCKS5 and HTTP proxies listening", slog.String("local", listener.Addr().String()))
	} else {
		if *flagSOCKS5Addr != "" {
			listener, err := net.Listen("tcp", *flagSOCKS5Addr)
			if err != nil {
				slog.Error("failed to listen for SOCKS5", slog.String("addr", *flagSOCKS5Addr), slog.Any("error", err))
				os.Exit(1)
			}
			closers = append(closers, listener)
			if verboseProxy {
				listener = loggingListener{Listener: listener, proxy: "socks5"}
			}
			startSOCKS5Proxy(listener, dialer, serveErr)
			slog.Info("SOCKS5 proxy listening", slog.String("local", listener.Addr().String()))
		}
		if *flagHTTPAddr != "" {
			listener, err := net.Listen("tcp", *flagHTTPAddr)
			if err != nil {
				slog.Error("failed to listen for HTTP proxy", slog.String("addr", *flagHTTPAddr), slog.Any("error", err))
				os.Exit(1)
			}
			closers = append(closers, listener)
			if verboseProxy {
				listener = loggingListener{Listener: listener, proxy: "http"}
			}
			startHTTPProxy(listener, dialer, serveErr)
			slog.Info("HTTP proxy listening", slog.String("local", listener.Addr().String()))
		}
	}

	select {
	case <-ctx.Done():
		for _, closer := range closers {
			if err := closer.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				slog.Warn("error closing listener", slog.Any("error", err))
			}
		}
	case result := <-serveErr:
		if result.err != nil && !errors.Is(result.err, net.ErrClosed) && !errors.Is(result.err, http.ErrServerClosed) {
			slog.Error("proxy stopped with error", slog.String("proxy", result.name), slog.Any("error", result.err))
			os.Exit(1)
		}
	}
}

func useExitNodeIfAvailable(ctx context.Context, lc *local.Client) error {
	suggestion, err := lc.SuggestExitNode(ctx)
	if err != nil {
		return fmt.Errorf("suggest exit node: %w", err)
	}
	if suggestion.ID == "" {
		slog.Info("no tailnet exit node available")
		return nil
	}

	prefs, err := lc.EditPrefs(ctx, &ipn.MaskedPrefs{
		Prefs:           ipn.Prefs{AutoExitNode: ipn.AnyExitNode},
		AutoExitNodeSet: true,
	})
	if err != nil {
		return fmt.Errorf("enable automatic exit node: %w", err)
	}

	slog.Info("automatic tailnet exit node enabled",
		slog.String("suggested_exit_node", string(suggestion.ID)),
		slog.String("suggested_exit_node_name", suggestion.Name),
		slog.String("selected_exit_node", string(prefs.ExitNodeID)),
	)
	return nil
}

func (d *tailnetDialer) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	resolvedAddr, resolved, err := d.resolveAddr(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	if !resolved {
		slog.Debug("dialing through tailnet with original host",
			slog.String("network", network),
			slog.String("addr", addr),
		)
		return d.ts.Dial(ctx, network, addr)
	}
	slog.Debug("dialing through tailnet",
		slog.String("network", network),
		slog.String("addr", addr),
		slog.String("resolved_addr", resolvedAddr),
	)
	return d.ts.Dial(ctx, network, resolvedAddr)
}

func (d *tailnetDialer) resolveAddr(ctx context.Context, network, addr string) (resolvedAddr string, resolved bool, err error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", false, err
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return net.JoinHostPort(ip.String(), port), true, nil
	}

	status, err := d.lc.Status(ctx)
	if err != nil {
		slog.Debug("tailnet status lookup failed", slog.String("host", host), slog.Any("error", err))
	}
	ips := lookupMagicDNS(status, network, host)
	if len(ips) == 0 {
		ips, err = d.lookupIP(ctx, network, host)
	}
	if err != nil {
		if isStrictTailnetHost(status, host) {
			return "", false, fmt.Errorf("tailnet DNS lookup for %q: %w", host, err)
		}
		slog.Debug("tailnet DNS lookup failed; falling back to normal tsnet dial",
			slog.String("host", host),
			slog.Any("error", err),
		)
		return "", false, nil
	}
	if len(ips) == 0 {
		if isStrictTailnetHost(status, host) {
			return "", false, fmt.Errorf("tailnet DNS lookup for %q returned no addresses", host)
		}
		return "", false, nil
	}

	resolvedAddr = net.JoinHostPort(ips[0].String(), port)
	slog.Debug("tailnet DNS resolved",
		slog.String("host", host),
		slog.String("network", network),
		slog.String("resolved_addr", resolvedAddr),
	)
	return resolvedAddr, true, nil
}

func lookupMagicDNS(status *ipnstate.Status, network, host string) []netip.Addr {
	if status == nil {
		return nil
	}
	hostKey := dnsMapKey(host)
	var ips []netip.Addr
	addPeerIPs := func(peer *ipnstate.PeerStatus) {
		if peer == nil || !peerMatchesHost(status, peer, hostKey) {
			return
		}
		for _, ip := range peer.TailscaleIPs {
			if ipMatchesNetwork(network, ip) {
				ips = append(ips, ip)
			}
		}
	}

	addPeerIPs(status.Self)
	for _, peer := range status.Peer {
		addPeerIPs(peer)
	}
	if len(ips) == 0 {
		return nil
	}

	slog.Debug("tailnet status resolved",
		slog.String("host", host),
		slog.String("network", network),
		slog.String("ips", fmt.Sprint(ips)),
	)
	return ips
}

func peerMatchesHost(status *ipnstate.Status, peer *ipnstate.PeerStatus, hostKey string) bool {
	for _, name := range peerDNSNames(status, peer) {
		if dnsMapKey(name) == hostKey {
			return true
		}
	}
	return false
}

func peerDNSNames(status *ipnstate.Status, peer *ipnstate.PeerStatus) []string {
	var names []string
	if peer.DNSName != "" {
		names = append(names, peer.DNSName)
	}
	if peer.HostName != "" {
		names = append(names, peer.HostName)
	}

	for _, suffix := range magicDNSSuffixes(status) {
		for _, name := range slices.Clone(names) {
			name = strings.TrimSuffix(name, ".")
			if name == "" {
				continue
			}
			if strings.HasSuffix(dnsMapKey(name), "."+dnsMapKey(suffix)) {
				short := strings.TrimSuffix(name, "."+strings.TrimSuffix(suffix, "."))
				names = append(names, short)
			} else if peer.HostName != "" {
				names = append(names, strings.TrimSuffix(peer.HostName, ".")+"."+strings.TrimSuffix(suffix, "."))
			}
		}
	}

	return names
}

func magicDNSSuffixes(status *ipnstate.Status) []string {
	if status == nil {
		return nil
	}
	var suffixes []string
	if status.MagicDNSSuffix != "" {
		suffixes = append(suffixes, status.MagicDNSSuffix)
	}
	if status.CurrentTailnet != nil && status.CurrentTailnet.MagicDNSSuffix != "" {
		suffixes = append(suffixes, status.CurrentTailnet.MagicDNSSuffix)
	}
	return suffixes
}

func isStrictTailnetHost(status *ipnstate.Status, host string) bool {
	hostKey := dnsMapKey(host)
	if !strings.Contains(hostKey, ".") {
		return true
	}
	for _, suffix := range magicDNSSuffixes(status) {
		suffixKey := dnsMapKey(suffix)
		if hostKey == suffixKey || strings.HasSuffix(hostKey, "."+suffixKey) {
			return true
		}
	}
	return strings.HasSuffix(hostKey, ".ts.net")
}

func dnsMapKey(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, "."))
}

func ipMatchesNetwork(network string, ip netip.Addr) bool {
	switch {
	case strings.HasSuffix(network, "4"):
		return ip.Is4()
	case strings.HasSuffix(network, "6"):
		return ip.Is6()
	default:
		return true
	}
}

func (d *tailnetDialer) lookupIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	var ips []netip.Addr
	var errs []error
	for _, queryType := range queryTypesForNetwork(network) {
		response, resolvers, err := d.lc.QueryDNS(ctx, dnsQueryName(host), queryType)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s query: %w", queryType, err))
			continue
		}
		queryIPs, err := parseDNSIPs(response, queryType)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s response: %w", queryType, err))
			continue
		}
		slog.Debug("tailnet DNS query complete",
			slog.String("host", host),
			slog.String("type", queryType),
			slog.Int("answers", len(queryIPs)),
			slog.String("resolvers", fmt.Sprint(resolvers)),
		)
		ips = append(ips, queryIPs...)
	}
	if len(ips) > 0 {
		return ips, nil
	}
	return nil, errors.Join(errs...)
}

func dnsQueryName(host string) string {
	if strings.HasSuffix(host, ".") {
		return host
	}
	return host + "."
}

func queryTypesForNetwork(network string) []string {
	if strings.HasSuffix(network, "4") {
		return []string{"A"}
	}
	if strings.HasSuffix(network, "6") {
		return []string{"AAAA"}
	}
	return []string{"A", "AAAA"}
}

func parseDNSIPs(response []byte, queryType string) ([]netip.Addr, error) {
	var parser dnsmessage.Parser
	header, err := parser.Start(response)
	if err != nil {
		return nil, err
	}
	if header.RCode != dnsmessage.RCodeSuccess {
		return nil, fmt.Errorf("rcode %v", header.RCode)
	}
	if err := parser.SkipAllQuestions(); err != nil {
		return nil, err
	}

	var ips []netip.Addr
	for {
		answerHeader, err := parser.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			return ips, nil
		}
		if err != nil {
			return nil, err
		}

		switch {
		case queryType == "A" && answerHeader.Type == dnsmessage.TypeA:
			answer, err := parser.AResource()
			if err != nil {
				return nil, err
			}
			ips = append(ips, netip.AddrFrom4(answer.A))
		case queryType == "AAAA" && answerHeader.Type == dnsmessage.TypeAAAA:
			answer, err := parser.AAAAResource()
			if err != nil {
				return nil, err
			}
			ips = append(ips, netip.AddrFrom16(answer.AAAA))
		default:
			if err := parser.SkipAnswer(); err != nil {
				return nil, err
			}
		}
	}
}

func startSOCKS5Proxy(listener net.Listener, dialer func(context.Context, string, string) (net.Conn, error), serveErr chan<- serveResult) {
	proxy := &socks5.Server{
		Logf: func(format string, args ...any) {
			slog.Debug(fmt.Sprintf(format, args...))
		},
		Dialer: dialer,
	}
	go func() {
		serveErr <- serveResult{name: "socks5", err: proxy.Serve(listener)}
	}()
}

func startHTTPProxy(listener net.Listener, dialer func(context.Context, string, string) (net.Conn, error), serveErr chan<- serveResult) {
	server := &http.Server{
		Handler: httpProxyHandler(dialer),
	}
	go func() {
		serveErr <- serveResult{name: "http", err: server.Serve(listener)}
	}()
}

func httpProxyHandler(dialer func(context.Context, string, string) (net.Conn, error)) http.Handler {
	proxy := &httputil.ReverseProxy{
		Director: func(r *http.Request) {},
		Transport: &http.Transport{
			DialContext: dialer,
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("HTTP proxy request", slog.String("method", r.Method), slog.String("target", r.RequestURI))

		if r.Method != http.MethodConnect {
			if strings.HasPrefix(r.RequestURI, "/") || r.RequestURI == "*" {
				http.Error(w, "bogus RequestURI; must be absolute URL or CONNECT", http.StatusBadRequest)
				return
			}
			proxy.ServeHTTP(w, r)
			return
		}

		dst := r.RequestURI
		backend, err := dialer(r.Context(), "tcp", dst)
		if err != nil {
			w.Header().Set("Tailscale-Connect-Error", err.Error())
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer backend.Close()

		client, clientBuf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer client.Close()

		if _, err := io.WriteString(client, "HTTP/1.1 200 OK\r\n\r\n"); err != nil {
			return
		}

		var clientSrc io.Reader = clientBuf
		if clientBuf.Reader.Buffered() == 0 {
			clientSrc = client
		}

		errc := make(chan error, 2)
		go func() {
			_, err := io.Copy(client, backend)
			errc <- err
		}()
		go func() {
			_, err := io.Copy(backend, clientSrc)
			errc <- err
		}()
		<-errc
	})
}
