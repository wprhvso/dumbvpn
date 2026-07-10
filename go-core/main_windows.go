//go:build windows

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	serviceName = "DumbVPN"
	displayName = "DumbVPN"
	description = "DumbVPN userspace VPN service (GOST + wintun)"

	tunGateway = "10.0.0.1"
	tunIface   = "tungo"

	repoOwner = "wprhvso"
	repoName  = "dumbvpn"
)

var bypassRoutes = []struct{ dest, mask string }{
	{"10.0.0.0", "255.0.0.0"},
	{"172.16.0.0", "255.240.0.0"},
	{"192.168.0.0", "255.255.0.0"},
	{"100.64.0.0", "255.192.0.0"},
	{"169.254.0.0", "255.255.0.0"},
}

var (
	elog            debug.Log
	detectedGateway string
	monitorStop     chan struct{}
)

func init() {
	sendLog = windowsSendLog
	platformInit = extractWintun
	directDialContext = windowsDirectDial
}

func windowsSendLog(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if elog != nil {
		elog.Info(1, "[DumbVPN] "+msg)
		return
	}
	fmt.Printf("[DumbVPN] %s\n", msg)
}

func usage() {
	fmt.Printf(`DumbVPN — userspace VPN service

Usage: %s <command>

Commands:
  start       Start the service
  stop        Stop the service
  restart     Restart the service
  status      Show service status
  logs [n]    Show last n log entries (default 50)
  update      Download and install the latest release
  install     Install the service with auto-start (Automatic) and start it
  uninstall   Stop and remove the service
  run         Run the VPN engine (this is how the service launches)

Running with no command installs the service (elevates via UAC if needed).
Commands that manage the service require administrator privileges.
`, os.Args[0])
}

func isAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,
		2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0,
		&sid,
	)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)

	token := windows.Token(0)
	member, err := token.IsMember(sid)
	if err != nil {
		return false
	}
	return member
}

func relaunchElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)

	var params *uint16
	if len(args) > 0 {
		params, _ = windows.UTF16PtrFromString(strings.Join(args, " "))
	}

	cwd, _ := os.Getwd()
	dir, _ := windows.UTF16PtrFromString(cwd)

	return windows.ShellExecute(0, verb, file, params, dir, windows.SW_NORMAL)
}

func main() {
	if len(os.Args) < 2 {
		isService, err := svc.IsWindowsService()
		if err == nil && isService {
			runService()
			return
		}

		if !isAdmin() {
			fmt.Println("Administrator privileges required to install; requesting elevation...")
			if err := relaunchElevated([]string{"install"}); err != nil {
				fmt.Printf("Elevation failed: %v\n", err)
				os.Exit(1)
			}
			return
		}

		fmt.Println("No command given; installing DumbVPN service...")
		if err := installService(); err != nil {
			fmt.Printf("Install failed: %v\n", err)
			fmt.Println()
			usage()
			os.Exit(1)
		}
		fmt.Printf("Service %q installed and started (auto-start enabled).\n", serviceName)
		return
	}

	switch strings.ToLower(os.Args[1]) {
	case "start":
		if err := startService(); err != nil {
			fmt.Printf("Start failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service started.")
	case "stop":
		if err := stopService(); err != nil {
			fmt.Printf("Stop failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service stopped.")
	case "restart":
		if err := stopService(); err != nil {
			fmt.Printf("Stop failed: %v\n", err)
			os.Exit(1)
		}
		if err := startService(); err != nil {
			fmt.Printf("Start failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service restarted.")
	case "status":
		if err := printStatus(); err != nil {
			fmt.Printf("Status failed: %v\n", err)
			os.Exit(1)
		}
	case "logs":
		n := "50"
		if len(os.Args) >= 3 {
			n = os.Args[2]
		}
		if err := printLogs(n); err != nil {
			fmt.Printf("Logs failed: %v\n", err)
			os.Exit(1)
		}
	case "update":
		if err := runUpdate(); err != nil {
			fmt.Printf("Update failed: %v\n", err)
			os.Exit(1)
		}
	case "install":
		if err := installService(); err != nil {
			fmt.Printf("Install failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Service %q installed and started (auto-start enabled).\n", serviceName)
	case "uninstall", "remove":
		if err := removeService(); err != nil {
			fmt.Printf("Uninstall failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Service %q removed.\n", serviceName)
	case "run":
		runService()
	default:
		usage()
		os.Exit(2)
	}
}

func openService() (*mgr.Mgr, *mgr.Service, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to service manager (administrator privileges required?): %w", err)
	}
	s, err := m.OpenService(serviceName)
	if err != nil {
		m.Disconnect()
		return nil, nil, fmt.Errorf("service %q is not installed: %w", serviceName, err)
	}
	return m, s, nil
}

func startService() error {
	m, s, err := openService()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()

	status, err := s.Query()
	if err == nil && status.State == svc.Running {
		fmt.Println("Service is already running.")
		return nil
	}

	if err := s.Start(); err != nil {
		return err
	}
	return waitForState(s, svc.Running, 15*time.Second)
}

func stopService() error {
	m, s, err := openService()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()

	status, err := s.Query()
	if err == nil && status.State == svc.Stopped {
		fmt.Println("Service is already stopped.")
		return nil
	}

	if _, err := s.Control(svc.Stop); err != nil {
		return err
	}
	return waitForState(s, svc.Stopped, 15*time.Second)
}

func waitForState(s *mgr.Service, want svc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		status, err := s.Query()
		if err != nil {
			return err
		}
		if status.State == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for service state %d (current: %d)", want, status.State)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func printStatus() error {
	m, s, err := openService()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()

	status, err := s.Query()
	if err != nil {
		return err
	}

	stateNames := map[svc.State]string{
		svc.Stopped:         "Stopped",
		svc.StartPending:    "Starting...",
		svc.StopPending:     "Stopping...",
		svc.Running:         "Running",
		svc.ContinuePending: "Resuming...",
		svc.PausePending:    "Pausing...",
		svc.Paused:          "Paused",
	}
	name, ok := stateNames[status.State]
	if !ok {
		name = fmt.Sprintf("Unknown (%d)", status.State)
	}
	fmt.Printf("Service:  %s\nState:    %s\n", serviceName, name)

	if status.State == svc.Running {
		if ifc, err := net.InterfaceByName(tunIface); err == nil {
			fmt.Printf("Tunnel:   %s (index %d) is up\n", tunIface, ifc.Index)
		} else {
			fmt.Printf("Tunnel:   %s is NOT up\n", tunIface)
		}
	}
	return nil
}

func printLogs(n string) error {
	query := fmt.Sprintf("*[System[Provider[@Name='%s']]]", serviceName)
	cmd := exec.Command("wevtutil", "qe", "Application", "/q:"+query, "/c:"+n, "/rd:true", "/f:text")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runUpdate() error {
	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/main/install.ps1", repoOwner, repoName)
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN environment variable is not set (required for private repo)")
	}
	script := fmt.Sprintf(
		"$env:GITHUB_TOKEN = '%s'; irm -Headers @{Authorization=\"token %s\"} '%s' | iex",
		token, token, rawURL,
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runService() {
	isService, err := svc.IsWindowsService()
	if err != nil {
		fmt.Printf("Failed to determine launch mode: %v\n", err)
		os.Exit(1)
	}

	if isService {
		el, err := eventlog.Open(serviceName)
		if err == nil {
			elog = el
			defer el.Close()
		}
		if err := svc.Run(serviceName, &vpnService{}); err != nil {
			windowsSendLog("Service failed: %v", err)
			os.Exit(1)
		}
		return
	}

	elog = debug.New(serviceName)
	windowsSendLog("Running in interactive (debug) mode. Press Ctrl+C to stop.")
	if err := debug.Run(serviceName, &vpnService{}); err != nil {
		windowsSendLog("Debug run failed: %v", err)
		os.Exit(1)
	}
}

type vpnService struct{}

func (m *vpnService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	changes <- svc.Status{State: svc.StartPending}

	windowsSendLog("Service starting. Initializing network and VPN engine...")
	monitorStop = make(chan struct{})
	go setupWindowsNetwork(monitorStop)
	go startVpnEngine(0)

	changes <- svc.Status{State: svc.Running, Accepts: accepted}
	windowsSendLog("Service started.")

loop:
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			windowsSendLog("Received stop request (%v). Shutting down...", c.Cmd)
			break loop
		default:
			windowsSendLog("Unexpected control request: %d", c.Cmd)
		}
	}

	changes <- svc.Status{State: svc.StopPending}
	close(monitorStop)
	stopVpnEngineInternal()
	cleanupWindowsNetwork()
	changes <- svc.Status{State: svc.Stopped}
	windowsSendLog("Service stopped.")
	return false, 0
}

func setupWindowsNetwork(stop chan struct{}) {
	var gateway string
	var ifIndex uint32
	var err error

	windowsSendLog("Waiting for physical default gateway to appear...")
	for i := 0; i < 300; i++ {
		gateway, ifIndex, err = detectDefaultGateway()
		if err == nil && gateway != "" {
			break
		}
		select {
		case <-stop:
			return
		case <-time.After(1 * time.Second):
		}
	}

	if gateway == "" {
		windowsSendLog("Failed to detect default gateway after retries: %v", err)
	} else {
		detectedGateway = gateway
		setPhysicalInterface(ifIndex)
		windowsSendLog("Detected physical gateway: %s (interface index %d)", gateway, ifIndex)
		addBypassRoutesWindows(gateway, ifIndex)
	}

	go monitorNetworkChanges(stop)

	windowsSendLog("Waiting for %s interface to come up...", tunIface)
	var tunIdx int
	for i := 0; i < 600; i++ {
		if ifc, err := net.InterfaceByName(tunIface); err == nil {
			tunIdx = ifc.Index
			break
		}
		select {
		case <-stop:
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	if tunIdx == 0 {
		windowsSendLog("Timed out waiting for %s interface.", tunIface)
		return
	}
	windowsSendLog("%s interface is up (index %d). Activating global routing...", tunIface, tunIdx)
	addSplitDefaultRoutes(tunIdx)
	setInterfaceDNS(tunIface, tunGateway)
	windowsSendLog("VPN is fully established and routed.")
}

func cleanupWindowsNetwork() {
	windowsSendLog("Cleaning up network configuration...")
	delSplitDefaultRoutes()
	if detectedGateway != "" {
		delBypassRoutesWindows(detectedGateway)
	}
	detectedGateway = ""
	setPhysicalInterface(0)
}

func monitorNetworkChanges(stop chan struct{}) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			gateway, ifIndex, err := detectDefaultGateway()
			if err != nil || gateway == "" {
				continue
			}
			if gateway != detectedGateway {
				windowsSendLog("Physical gateway changed: %q -> %q. Reconfiguring...", detectedGateway, gateway)
				if detectedGateway != "" {
					delBypassRoutesWindows(detectedGateway)
				}
				detectedGateway = gateway
				setPhysicalInterface(ifIndex)
				addBypassRoutesWindows(gateway, ifIndex)
			}
			ensureTunnelRouting()
		}
	}
}

func ensureTunnelRouting() {
	ifc, err := net.InterfaceByName(tunIface)
	if err != nil {
		return
	}
	if splitDefaultRoutesPresent() {
		return
	}
	windowsSendLog("%s is up but split default routes are missing. Restoring routing...", tunIface)
	addSplitDefaultRoutes(ifc.Index)
	setInterfaceDNS(tunIface, tunGateway)
	windowsSendLog("Split default routes and DNS restored.")
}

func splitDefaultRoutesPresent() bool {
	out, err := exec.Command("route.exe", "print", "-4").Output()
	if err != nil {
		return true
	}
	text := string(out)
	hasLow := false
	hasHigh := false
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[0] == "0.0.0.0" && fields[1] == "128.0.0.0" && fields[2] == tunGateway {
			hasLow = true
		}
		if fields[0] == "128.0.0.0" && fields[1] == "128.0.0.0" && fields[2] == tunGateway {
			hasHigh = true
		}
	}
	return hasLow && hasHigh
}

func detectDefaultGateway() (gateway string, ifIndex uint32, err error) {
	script := "$r = Get-NetRoute -DestinationPrefix '0.0.0.0/0' -ErrorAction Stop | " +
		"Where-Object { $_.NextHop -ne '0.0.0.0' -and $_.InterfaceAlias -ne '" + tunIface + "' } | " +
		"Sort-Object -Property RouteMetric | Select-Object -First 1; " +
		"if ($r) { \"$($r.NextHop)|$($r.ifIndex)\" }"

	out, cmdErr := exec.Command("powershell.exe", "-NoProfile", "-Command", script).Output()
	if cmdErr != nil {
		return "", 0, cmdErr
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", 0, fmt.Errorf("no default route found")
	}
	parts := strings.SplitN(line, "|", 2)
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("unexpected route output: %q", line)
	}
	gateway = strings.TrimSpace(parts[0])
	idx, convErr := strconv.Atoi(strings.TrimSpace(parts[1]))
	if convErr != nil {
		return "", 0, convErr
	}
	return gateway, uint32(idx), nil
}

func addBypassRoutesWindows(gateway string, ifIndex uint32) {
	for _, rt := range bypassRoutes {
		exec.Command("route.exe", "delete", rt.dest, "mask", rt.mask, gateway).Run()
		if err := exec.Command("route.exe", "add", rt.dest, "mask", rt.mask, gateway,
			"metric", "5", "if", strconv.FormatUint(uint64(ifIndex), 10)).Run(); err != nil {
			windowsSendLog("Warning: failed to add bypass route %s/%s: %v", rt.dest, rt.mask, err)
		} else {
			windowsSendLog("Bypass route added: %s/%s -> %s", rt.dest, rt.mask, gateway)
		}
	}
}

func delBypassRoutesWindows(gateway string) {
	for _, rt := range bypassRoutes {
		exec.Command("route.exe", "delete", rt.dest, "mask", rt.mask, gateway).Run()
	}
}

func addSplitDefaultRoutes(tunIdx int) {
	ifStr := strconv.Itoa(tunIdx)
	if err := exec.Command("route.exe", "add", "0.0.0.0", "mask", "128.0.0.0", tunGateway,
		"metric", "1", "if", ifStr).Run(); err != nil {
		windowsSendLog("Warning: failed to add split route 0.0.0.0/1: %v", err)
	}
	if err := exec.Command("route.exe", "add", "128.0.0.0", "mask", "128.0.0.0", tunGateway,
		"metric", "1", "if", ifStr).Run(); err != nil {
		windowsSendLog("Warning: failed to add split route 128.0.0.0/1: %v", err)
	}
}

func delSplitDefaultRoutes() {
	exec.Command("route.exe", "delete", "0.0.0.0", "mask", "128.0.0.0").Run()
	exec.Command("route.exe", "delete", "128.0.0.0", "mask", "128.0.0.0").Run()
}

func setInterfaceDNS(iface, dns string) {
	if err := exec.Command("netsh", "interface", "ip", "set", "dns",
		fmt.Sprintf("name=%s", iface), "static", dns).Run(); err != nil {
		windowsSendLog("Warning: failed to set DNS on %s: %v", iface, err)
	}
}

func installService() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager (administrator privileges required?): %w", err)
	}
	defer m.Disconnect()

	if existing, err := m.OpenService(serviceName); err == nil {
		existing.Close()
		return fmt.Errorf("service %q is already installed; run 'uninstall' first", serviceName)
	}

	s, err := m.CreateService(serviceName, exePath, mgr.Config{
		DisplayName:      displayName,
		Description:      description,
		StartType:        mgr.StartAutomatic,
		DelayedAutoStart: true,
		Dependencies:     []string{"Tcpip", "Dnscache", "nsi"},
		ErrorControl:     mgr.ErrorNormal,
	})
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	defer s.Close()

	if err := s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}, 86400); err != nil {
		windowsSendLog("Warning: failed to set recovery actions: %v", err)
	}

	if err := eventlog.InstallAsEventCreate(serviceName, eventlog.Info|eventlog.Warning|eventlog.Error); err != nil {
		windowsSendLog("Warning: failed to install event log source: %v", err)
	}

	if err := s.Start(); err != nil {
		return fmt.Errorf("service installed but failed to start: %w", err)
	}

	return waitForState(s, svc.Running, 15*time.Second)
}

func removeService() error {
	m, s, err := openService()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()

	status, err := s.Query()
	if err == nil && status.State != svc.Stopped {
		if _, err := s.Control(svc.Stop); err != nil {
			windowsSendLog("Warning: failed to stop service before removal: %v", err)
		} else {
			waitForState(s, svc.Stopped, 15*time.Second)
		}
	}

	if err := s.Delete(); err != nil {
		return fmt.Errorf("failed to delete service: %w", err)
	}

	if err := eventlog.Remove(serviceName); err != nil {
		windowsSendLog("Warning: failed to remove event log source: %v", err)
	}

	return nil
}
