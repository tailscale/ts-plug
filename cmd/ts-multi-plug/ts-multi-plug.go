// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/tsnet"
)

var (
	flagHostname   string
	flagDir        string
	flagLogLevel   string
	flagDebugTSNet = flag.Bool("debug-tsnet", false, "enable tsnet.Server logging")

	// HTTP flags
	httpEnable = flag.Bool("http", false, "Enable HTTP listener (default 80:8080)")
	flagHttp   = NewPortMapFlag(80, 8080)

	// HTTPS flags
	httpsEnable = flag.Bool("https", false, "Enable HTTPS listener (default 443:8080)")
	flagHttps   = NewPortMapFlag(443, 8080)

	// DNS flags
	dnsEnable = flag.Bool("dns", false, "Enable DNS listener (default 53:53)")
	flagDNS   = NewPortMapFlag(53, 53)

	flagPublic = flag.Bool("public", false, "Enable public https access")

	flagDialTimeout           = flag.Duration("dial-timeout", 2*time.Second, "Reverse-proxy dial timeout to the upstream (0 = no limit)")
	flagResponseHeaderTimeout = flag.Duration("response-header-timeout", time.Second, "Reverse-proxy response-header timeout (0 = no limit; useful for streaming / slow-first-byte upstreams)")
	flagIdleConnTimeout       = flag.Duration("idle-conn-timeout", 0, "Reverse-proxy idle connection timeout (0 = no limit)")
)

func init() {
	flag.Var(flagHttp, "http-port", "HTTP port mapping (in:out or port)")
	flag.Var(flagHttps, "https-port", "HTTPS port mapping (in:out or port)")
	flag.Var(flagDNS, "dns-port", "DNS port mapping (in:out or port)")

	flag.StringVar(&flagHostname, "hostname", "tsmultiplug", "hostname on tailnet")
	flag.StringVar(&flagHostname, "hn", "tsmultiplug", "hostname on tailnet (short)")
	flag.StringVar(&flagDir, "dir", ".data", "directory to store tailscale state")
	flag.StringVar(&flagLogLevel, "log", "info", "Log level (debug | info | warn | error)")
}

func main() {
	flag.Parse()

	// Set log level
	switch flagLogLevel {
	case "debug":
		slog.SetLogLoggerLevel(slog.LevelDebug)
	case "info":
		slog.SetLogLoggerLevel(slog.LevelInfo)
	case "warn":
		slog.SetLogLoggerLevel(slog.LevelWarn)
	case "error":
		slog.SetLogLoggerLevel(slog.LevelError)
	default:
		slog.Error("unknown log level", slog.String("level", flagLogLevel))
		os.Exit(1)
	}

	// Everything after "--" goes into cmdArgs
	cmdArgs := flag.Args()
	if len(cmdArgs) == 0 {
		slog.Error("no command to run")
		os.Exit(1)
	}

	for name, d := range map[string]time.Duration{
		"dial-timeout":            *flagDialTimeout,
		"response-header-timeout": *flagResponseHeaderTimeout,
		"idle-conn-timeout":       *flagIdleConnTimeout,
	} {
		if d < 0 {
			slog.Error("negative duration not allowed", "flag", name, "value", d)
			os.Exit(1)
		}
	}

	// Check that at least one listener is enabled
	if !flagHttp.IsSet() && !flagHttps.IsSet() && !flagDNS.IsSet() {
		slog.Info("no listeners enabled, using HTTPS by default")
		*httpsEnable = true
	}

	// If boolean flag is set but no custom port, use defaults
	if *httpEnable && !flagHttp.IsSet() {
		flagHttp.Set("")
	}
	if *httpsEnable && !flagHttps.IsSet() {
		flagHttps.Set("")
	}
	if *dnsEnable && !flagDNS.IsSet() {
		flagDNS.Set("")
	}

	// cmdExitChannel receives the error when cmd.Wait() return
	cmdExitChan := make(chan error)

	// signalChan receives OS signals for shutdown
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// create a context that can be cancelled to stop upstream and tsnet
	ctx, cancelCtx := context.WithCancel(context.Background())

	// start the child process that will handle requests
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = append(os.Environ(), "TSPLUG_ACTIVE=1")
	if err := attachLogging(cmd); err != nil {
		slog.Error("failed to attach logging to cmd", "error", err)
		os.Exit(1)
	}

	slog.Info("starting command", "cmd", strings.Join(cmdArgs, " "))
	if err := cmd.Start(); err != nil {
		slog.Error("command start failed", "error", err)
		os.Exit(1)
	} else {
		slog.Info("command started")
	}

	// handle the exit cases either from signal or the upstream command exiting
	go func() {
		for {
			select {
			case cmdExitChan <- cmd.Wait():
				// the upstream command has exited
				return
			case sig := <-signalChan:
				slog.Info("signal received, shutting down...", "sig", sig.String())

				// this will cause the case above with cmd.Wait() to return
				// as well ts.Up() to exit early if it hasn't been fully initialized yet
				cancelCtx()
			}
		}
	}()

	ts := &tsnet.Server{
		Hostname: flagHostname,
		Dir:      flagDir,
	}

	if *flagDebugTSNet {
		ts.Logf = func(format string, args ...any) {
			cur := slog.SetLogLoggerLevel(slog.LevelDebug) // force debug if this option is on
			slog.Debug(fmt.Sprintf(format, args...))
			slog.SetLogLoggerLevel(cur)
		}
	}

	// start the tsnet server. ts.Up() blocks in a loop calling watcher.Next()
	// which performs json.Decoder.Decode() on an HTTP response body stream. The
	// cancellable context passed here ensures that when the signal handler calls
	// cancelCtx() (lines 89-94), the underlying HTTP request is cancelled, causing
	// the Decode() to return an error and ts.Up() to exit early. Without a
	// cancellable context, ts.Up() would hang indefinitely on SIGINT/SIGTERM
	st, err := ts.Up(ctx)
	if err != nil {
		slog.Error("error starting tsnet server", slog.Any("error", err))
		cancelCtx()
		os.Exit(1)
	}

	lc, err := ts.LocalClient()
	if err != nil {
		slog.Error("Failed to get tsnet LocalClient", "error", err)
		cancelCtx()
		os.Exit(1)
	}

	hostname := strings.TrimSuffix(st.Self.DNSName, ".")

	// Start HTTP listener if enabled
	if flagHttp.IsSet() {
		go func() {
			if err := startHTTPListener(ctx, ts, lc, hostname, flagHttp); err != nil {
				slog.Error("HTTP listener failed", "error", err)
				cancelCtx()
			}
		}()
	}

	// Start HTTPS listener if enabled
	if flagHttps.IsSet() {
		go func() {
			if err := startHTTPSListener(ctx, ts, lc, hostname, flagHttps, *flagPublic); err != nil {
				slog.Error("HTTPS listener failed", "error", err)
				cancelCtx()
			}
		}()
	}

	// Start DNS listener if enabled
	if flagDNS.IsSet() {
		go func() {
			if err := startDNSListener(ctx, ts, lc, hostname, flagDNS); err != nil {
				slog.Error("DNS listener failed", "error", err)
				cancelCtx()
			}
		}()
	}

	err = <-cmdExitChan
	slog.Info("cmd exited", "error", err)
}

// startHTTPListener starts an HTTP listener on the tailnet
func startHTTPListener(ctx context.Context, ts *tsnet.Server, lc *local.Client, hostname string, portMap *PortMapFlag) error {
	listener, err := ts.Listen("tcp", fmt.Sprintf(":%d", portMap.In))
	if err != nil {
		return fmt.Errorf("failed to listen on HTTP port %d: %w", portMap.In, err)
	}
	defer listener.Close()

	slog.Info(fmt.Sprintf("listening at (HTTP): http://%s:%d", hostname, portMap.In))

	proxy := createReverseProxy(portMap.Out)
	whoisHandler := createWhoisHandler(lc, proxy)

	httpServer := &http.Server{
		Handler: whoisHandler,
	}

	go func() {
		<-ctx.Done()
		httpServer.Close()
	}()

	if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("HTTP server error: %w", err)
	}

	return nil
}

// startHTTPSListener starts an HTTPS listener on the tailnet
func startHTTPSListener(ctx context.Context, ts *tsnet.Server, lc *local.Client, hostname string, portMap *PortMapFlag, useFunnel bool) error {
	var listener net.Listener
	var err error

	if useFunnel {
		listener, err = ts.ListenFunnel("tcp", ":443")
		if err != nil {
			return fmt.Errorf("failed to listen to funnel port 443")
		}
		defer listener.Close()
		slog.Info(fmt.Sprintf("listening at (FUNNEL HTTPS): https://%s", hostname))

	} else {
		listener, err = ts.ListenTLS("tcp", fmt.Sprintf(":%d", portMap.In))
		if err != nil {
			return fmt.Errorf("failed to listen on HTTPS port %d: %w", portMap.In, err)
		}
		defer listener.Close()
		slog.Info(fmt.Sprintf("listening at (HTTPS): https://%s:%d", hostname, portMap.In))
	}

	proxy := createReverseProxy(portMap.Out)
	whoisHandler := createWhoisHandler(lc, proxy)

	httpServer := &http.Server{
		Handler: whoisHandler,
	}

	go func() {
		<-ctx.Done()
		httpServer.Close()
	}()

	if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("HTTPS server error: %w", err)
	}

	return nil
}

// startDNSListener starts a DNS packet forwarder on the tailnet
func startDNSListener(ctx context.Context, ts *tsnet.Server, lc *local.Client, hostname string, portMap *PortMapFlag) error {
	// Get Tailscale status to retrieve our IP address
	status, err := lc.Status(ctx)
	if err != nil {
		return fmt.Errorf("failed to get tailscale status: %w", err)
	}
	if len(status.TailscaleIPs) == 0 {
		return fmt.Errorf("no tailscale IPs available")
	}

	// Use the first Tailscale IP (typically IPv4)
	tsIP := status.TailscaleIPs[0]

	// Listen on tailnet side
	tsConn, err := ts.ListenPacket("udp", fmt.Sprintf("%s:%d", tsIP, portMap.In))
	if err != nil {
		return fmt.Errorf("failed to listen on DNS port %d: %w", portMap.In, err)
	}
	defer tsConn.Close()

	slog.Info(fmt.Sprintf("listening at (DNS): %s:%d -> 127.0.0.1:%d", hostname, portMap.In, portMap.Out))

	// Create upstream connection to localhost DNS
	upstreamAddr := fmt.Sprintf("127.0.0.1:%d", portMap.Out)

	// Buffer for DNS packets (DNS typically uses 512 bytes, but we'll support larger)
	buffer := make([]byte, 4096)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			// Set read deadline to allow context cancellation
			slog.Debug("Waiting for DNS query...") //"
			tsConn.SetReadDeadline(time.Now().Add(1 * time.Second))

			n, clientAddr, err := tsConn.ReadFrom(buffer)
			slog.Debug("DNS query received", "client", clientAddr, "size", n)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue // timeout is expected, check context and retry
				}
				// Check if context was cancelled
				if ctx.Err() != nil {
					return nil
				}
				slog.Error("DNS read error", "error", err)
				continue
			}

			// Forward to upstream DNS server
			go handleDNSQuery(buffer[:n], clientAddr, tsConn, upstreamAddr)
		}
	}
}

// handleDNSQuery forwards a DNS query to upstream and sends response back
func handleDNSQuery(query []byte, clientAddr net.Addr, tsConn net.PacketConn, upstreamAddr string) {
	// Create connection to upstream DNS
	upstreamConn, err := net.Dial("udp", upstreamAddr)
	if err != nil {
		slog.Error("failed to connect to upstream DNS", "error", err, "upstream", upstreamAddr)
		return
	}
	defer upstreamConn.Close()

	// Set deadlines
	upstreamConn.SetDeadline(time.Now().Add(5 * time.Second))

	// Send query to upstream
	if _, err := upstreamConn.Write(query); err != nil {
		slog.Error("failed to write to upstream DNS", "error", err)
		return
	}

	// Read response from upstream
	response := make([]byte, 4096)
	n, err := upstreamConn.Read(response)
	if err != nil {
		slog.Error("failed to read from upstream DNS", "error", err)
		return
	}

	// Send response back to client
	if _, err := tsConn.WriteTo(response[:n], clientAddr); err != nil {
		slog.Error("failed to write response to client", "error", err)
		return
	}

	slog.Debug("DNS query handled", "client", clientAddr, "size", n)
}

// createReverseProxy creates a reverse proxy to the specified localhost port
func createReverseProxy(port int) *httputil.ReverseProxy {
	u, err := url.Parse(fmt.Sprintf("http://localhost:%d", port))
	if err != nil {
		slog.Error("invalid upstream", "error", err)
		os.Exit(1)
	}

	proxy := httputil.NewSingleHostReverseProxy(u)

	proxy.Transport = &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: *flagDialTimeout,
		}).DialContext,
		ResponseHeaderTimeout: *flagResponseHeaderTimeout,
		IdleConnTimeout:       *flagIdleConnTimeout,
	}

	return proxy
}

// createWhoisHandler creates an HTTP handler that injects Tailscale user information
func createWhoisHandler(lc *local.Client, proxy *httputil.ReverseProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var ul, dn, pp string

		who, err := lc.WhoIs(r.Context(), r.RemoteAddr)
		if err != nil {
			slog.Error("whois lookup failed", "error", err, "remote", r.RemoteAddr)
		} else if who.UserProfile != nil && who.UserProfile.LoginName != "tagged-devices" {
			slog.Debug("set Tailscale-* headers",
				slog.String("remote", r.RemoteAddr),
				slog.String("id", who.UserProfile.ID.String()),
			)

			ul = who.UserProfile.LoginName
			dn = who.UserProfile.DisplayName
			pp = who.UserProfile.ProfilePicURL
		}

		// always populate the headers, even if blank for security reasons.
		r.Header.Set("Tailscale-User-Login", ul)
		r.Header.Set("Tailscale-User-Name", dn)
		r.Header.Set("Tailscale-User-Profile-Pic", pp)

		proxy.ServeHTTP(w, r)
	}
}

// attachLogging attaches logging to a command's stdout and stderr
// and logs them to the slog logger.
// It returns an error if it fails to attach the pipes.
func attachLogging(cmd *exec.Cmd) error {

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	// log stdout
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			slog.Info(fmt.Sprintf("cmd > %s", scanner.Text()))
		}
		if err := scanner.Err(); err != nil {
			slog.Error("reading stdout failed", "error", err)
		}
	}()

	// log stderr
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			slog.Info(fmt.Sprintf("cmd stderr> %s", scanner.Text()))
		}
		if err := scanner.Err(); err != nil {
			slog.Error("reading stderr failed", "error", err)
		}
	}()

	return nil
}

type PortMapFlag struct {
	In    int
	Out   int
	isSet bool

	defaultIn  int
	defaultOut int
}

// NewPortMapFlag creates a new PortMapFlag with default in/out ports
func NewPortMapFlag(in, out int) *PortMapFlag {
	return &PortMapFlag{
		defaultIn:  in,
		defaultOut: out,
	}
}

func (p *PortMapFlag) String() string {
	if !p.isSet {
		return fmt.Sprintf("%d:%d", p.defaultIn, p.defaultOut)
	}
	return fmt.Sprintf("%d:%d", p.In, p.Out)
}

func (p *PortMapFlag) IsSet() bool {
	return p.isSet
}

func (p *PortMapFlag) Set(value string) error {
	p.isSet = true

	if value == "" {
		p.In = p.defaultIn
		p.Out = p.defaultOut
		return nil
	}

	// Parse the input string in the format "in:out" or "port"
	parts := strings.Split(value, ":")
	if len(parts) == 1 {
		// If only one part, treat it as both in and out
		port, err := strconv.Atoi(parts[0])
		if err != nil {
			return fmt.Errorf("invalid port format: %s", value)
		}
		p.In = port
		p.Out = port

	} else if len(parts) == 2 {
		// If two parts, parse as in:out
		inPort, err := strconv.Atoi(parts[0])
		if err != nil {
			return fmt.Errorf("invalid in port format: %s", parts[0])
		}
		outPort, err := strconv.Atoi(parts[1])
		if err != nil {
			return fmt.Errorf("invalid out port format: %s", parts[1])
		}
		p.In = inPort
		p.Out = outPort
	}
	return nil
}
