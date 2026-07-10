package main

import "fmt"

var (
	buildServerIP   = "127.0.0.1"
	buildServerPort = "8443"
	buildRelayUser  = "user"
	buildRelayPass  = "pass"
)
	
func serverAddr() string {
	return fmt.Sprintf("%s:%s", buildServerIP, buildServerPort)
}
