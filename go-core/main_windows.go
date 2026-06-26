//go:build windows

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	serviceName = "DumbVPN"
	displayName = "DumbVPN"
	description = "DumbVPN userspace VPN service (GOST + wintun)"

	serverIP = "79.137.207.89"

	tunGateway = "10.0.0.1"
	tunIface   = "tungo"
)

var bypassRoutes = []struct{ dest, mask string }{
	{"10.0.0.0", "255.0.0.0"},
	{"172.16.0.0", "255.240.0.0"},
	{"192.168.0.0", "255.255.0.0"},
	{"100.64.0.0", "255.192.0.0"},
	{"169.254.0.0", "255.255.0.0"},
}

var elog debug.Log

func init() {
	sendLog = windowsSendLog
	platformInit = func() {}
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
  install     Install the service with auto-start (Automatic) and start it
  uninstall   Stop and remove the service
  run         Run the VPN engine (this is how the service and auto-start launch)

The install/uninstall commands and the actual runtime require administrator privileges.
`, os.Args[0])
}

func main() {
	if len(os.Args) < 2 {
		isService, err := svc.IsWindowsService()
		if err == nil && isService {
			runService()
			return
		}
		usage()
		return
	}

	switch strings.ToLower(os.Args[1]) {
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
	setupWindowsNetwork()
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
	stopVpnEngineInternal()
	cleanupWindowsNetwork()
	changes <- svc.Status{State: svc.Stopped}
	windowsSendLog("Service stopped.")
	return false, 0
}

func installService() error {
	exepath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to determine executable path: %w", err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager (administrator privileges required?): %w", err)
	}
	defer m.Disconnect()

	if s, err := m.OpenService(serviceName); err == nil {
		s.Close()
		return fmt.Errorf("service %q is already installed", serviceName)
	}

	s, err := m.CreateService(serviceName, exepath, mgr.Config{
		DisplayName:  displayName,
		Description:  description,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
	}, "run")
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	defer s.Close()

	if err := eventlog.InstallAsEventCreate(serviceName, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil {
		fmt.Printf("Warning: failed to register Event Log source: %v\n", err)
	}

	if err := s.Start(); err != nil {
		return fmt.Errorf("service created but failed to start: %w", err)
	}

	return nil
}

func removeService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager (administrator privileges required?): %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q is not installed: %w", serviceName, err)
	}
	defer s.Close()

	if status, err := s.Control(svc.Stop); err == nil {
		timeout := time.Now().Add(10 * time.Second)
		for status.State != svc.Stopped {
			if time.Now().After(timeout) {
				break
			}
			time.Sleep(300 * time.Millisecond)
			status, err = s.Query()
			if err != nil {
				break
			}
		}
	}

	if err := s.Delete(); err != nil {
		return fmt.Errorf("failed to delete service: %w", err)
	}

	if err := eventlog.Remove(serviceName); err != nil {
		fmt.Printf("Warning: failed to remove Event Log source: %v\n", err)
	}

	return nil
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		windowsSendLog("Command %q %v failed: %v (%s)", name, args, err, strings.TrimSpace(string(out)))
	}
	return err
}

func detectGateway() (string, error) {
	out, err := exec.Command("route", "print", "-4").Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == "0.0.0.0" && fields[1] == "0.0.0.0" {
			gw := fields[2]
			if net.ParseIP(gw) != nil {
				return gw, nil
			}
		}
	}
	return "", fmt.Errorf("failed to find default gateway in route print output")
}

func setupWindowsNetwork() {
	gateway, err := detectGateway()
	if err != nil {
		windowsSendLog("CRITICAL: failed to detect physical gateway: %v", err)
		return
	}
	windowsSendLog("Detected physical gateway: %s", gateway)

	runCmd("route", "delete", serverIP)
	if err := runCmd("route", "add", serverIP, "mask", "255.255.255.255", gateway); err == nil {
		windowsSendLog("Server route pinned: %s -> %s", serverIP, gateway)
	}

	for _, b := range bypassRoutes {
		runCmd("route", "delete", b.dest)
		if err := runCmd("route", "add", b.dest, "mask", b.mask, gateway); err == nil {
			windowsSendLog("Bypass route added: %s/%s -> %s", b.dest, b.mask, gateway)
		}
	}

	go func() {
		windowsSendLog("Waiting for the %s interface to be created by GOST/wintun...", tunIface)
		var idx int
		for {
			if ifc, err := net.InterfaceByName(tunIface); err == nil {
				idx = ifc.Index
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		windowsSendLog("Interface %s detected (index %d)! Activating global routing...", tunIface, idx)

		ifStr := fmt.Sprintf("%d", idx)
		runCmd("route", "add", "0.0.0.0", "mask", "128.0.0.0", tunGateway, "if", ifStr, "metric", "1")
		runCmd("route", "add", "128.0.0.0", "mask", "128.0.0.0", tunGateway, "if", ifStr, "metric", "1")

		runCmd("netsh", "interface", "ipv4", "set", "dns", "name="+tunIface, "static", tunGateway)

		windowsSendLog("VPN is fully established and routed. Enjoy!")
	}()
}

func cleanupWindowsNetwork() {
	windowsSendLog("Cleaning up network routes...")
	runCmd("route", "delete", "0.0.0.0", "mask", "128.0.0.0")
	runCmd("route", "delete", "128.0.0.0", "mask", "128.0.0.0")
	runCmd("route", "delete", serverIP)
	for _, b := range bypassRoutes {
		runCmd("route", "delete", b.dest)
	}
	runCmd("netsh", "interface", "ipv4", "set", "dns", "name="+tunIface, "dhcp")
	windowsSendLog("Cleanup completed.")
}
