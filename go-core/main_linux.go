//go:build !android

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

var (
	detectedGateway string
	detectedIface   string
)

func init() {
	sendLog = linuxSendLog
	platformInit = func() {}
}

func linuxSendLog(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("[DumbVPN] %s\n", msg)
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Run()
}

func detectNetwork() (gateway string, iface string, err error) {
	out, err := exec.Command("ip", "route", "get", "1.1.1.1").Output()
	if err != nil {
		return "", "", err
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "via" && i+1 < len(fields) {
			gateway = fields[i+1]
		}
		if f == "dev" && i+1 < len(fields) {
			iface = fields[i+1]
		}
	}
	if gateway == "" || iface == "" {
		return "", "", fmt.Errorf("failed to parse route from: %s", string(out))
	}
	return gateway, iface, nil
}

func main() {
	if os.Getenv("GODEBUG") != "netdns=go" {
		os.Setenv("GODEBUG", "netdns=go")
		argv0, err := exec.LookPath(os.Args[0])
		if err == nil {
			linuxSendLog("Re-executing with GODEBUG=netdns=go...")
			syscall.Exec(argv0, os.Args, os.Environ())
		}
	}

	linuxSendLog("Setting effective Group ID to 10001 for policy routing...")
	if err := syscall.Setresgid(10001, 10001, 10001); err != nil {
		linuxSendLog("Failed to set GID to 10001: %v. Make sure you run as root!", err)
		os.Exit(1)
	}

	gateway, iface, err := detectNetwork()
	if err != nil {
		linuxSendLog("CRITICAL: Failed to detect network gateway: %v", err)
		os.Exit(1)
	}
	detectedGateway = gateway
	detectedIface = iface
	linuxSendLog("Detected physical gateway: %s on interface %s", gateway, iface)

	linuxSendLog("Initializing routing tables and iptables rules...")
	runCmd("ip", "route", "add", "default", "via", gateway, "dev", iface, "table", "100")
	runCmd("ip", "route", "add", "192.168.10.0/24", "dev", iface, "table", "100")
	runCmd("ip", "route", "add", "79.137.207.89", "via", gateway, "dev", iface)

	runCmd("ip", "rule", "add", "fwmark", "0x1", "table", "100")
	
	runCmd("iptables", "-t", "mangle", "-D", "OUTPUT", "-m", "owner", "--gid-owner", "10001", "-j", "MARK", "--set-mark", "0x1")
	runCmd("iptables", "-t", "mangle", "-A", "OUTPUT", "-m", "owner", "--gid-owner", "10001", "-j", "MARK", "--set-mark", "0x1")
	
	runCmd("iptables", "-t", "nat", "-D", "POSTROUTING", "-o", iface, "-j", "MASQUERADE")
	runCmd("iptables", "-t", "nat", "-A", "POSTROUTING", "-o", iface, "-j", "MASQUERADE")

	runCmd("sysctl", "-w", "net.ipv4.conf.all.accept_local=1")
	runCmd("sysctl", "-w", "net.ipv4.conf.all.rp_filter=0")
	runCmd("sysctl", "-w", "net.ipv4.conf."+iface+".accept_local=1")
	runCmd("sysctl", "-w", "net.ipv4.conf."+iface+".rp_filter=0")

	go func() {
		linuxSendLog("Waiting for tungo interface to be created by GOST...")
		for {
			if _, err := net.InterfaceByName("tungo"); err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		linuxSendLog("tungo interface detected! Activating global routing...")
		runCmd("ip", "route", "add", "0.0.0.0/1", "dev", "tungo")
		runCmd("ip", "route", "add", "128.0.0.0/1", "dev", "tungo")
		runCmd("resolvectl", "dns", "tungo", "10.0.0.1")
		runCmd("resolvectl", "domain", "tungo", "~.")
		linuxSendLog("VPN is fully established and routed. Enjoy!")
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go startVpnEngine(0)

	sig := <-sigs
	linuxSendLog("Received signal: %v. Cleaning up network rules...", sig)

	runCmd("ip", "route", "del", "0.0.0.0/1", "dev", "tungo")
	runCmd("ip", "route", "del", "128.0.0.0/1", "dev", "tungo")
	runCmd("resolvectl", "revert", "tungo")
	runCmd("ip", "route", "del", "79.137.207.89", "via", detectedGateway, "dev", detectedIface)
	runCmd("iptables", "-t", "mangle", "-D", "OUTPUT", "-m", "owner", "--gid-owner", "10001", "-j", "MARK", "--set-mark", "0x1")
	runCmd("iptables", "-t", "nat", "-D", "POSTROUTING", "-o", detectedIface, "-j", "MASQUERADE")
	runCmd("ip", "rule", "del", "fwmark", "0x1", "table", "100")
	runCmd("ip", "route", "flush", "table", "100")

	linuxSendLog("Cleanup completed. Exiting.")
	os.Exit(0)
}
