//go:build android

package main

/*
#cgo LDFLAGS: -llog

#include <jni.h>
#include <android/log.h>
#include <stdlib.h>

static inline void log_info(const char* msg) {
    __android_log_write(ANDROID_LOG_INFO, "GoCore", msg);
}
*/
import "C"

import (
	"bufio"
	"fmt"
	"os"
	"unsafe"
)

func init() {
	sendLog = androidSendLog
	platformInit = redirectLogs
}

func androidSendLog(format string, args ...interface{}) {
	msg := "[fd] " + fmt.Sprintf(format, args...)
	cMsg := C.CString(msg)
	defer C.free(unsafe.Pointer(cMsg))
	C.log_info(cMsg)
}

func redirectLogs() {
	r, w, err := os.Pipe()
	if err != nil {
		return
	}
	os.Stdout = w
	os.Stderr = w

	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			sendLog("%s", scanner.Text())
		}
	}()
}

//export Java_ru_murasya_vpn_MyVpnService_startGoCore
func Java_ru_murasya_vpn_MyVpnService_startGoCore(env *C.JNIEnv, clazz C.jclass, fd C.jint) {
	vpnFd := int(fd)
	sendLog("Received FD %d from Android VpnService.", vpnFd)
	go startVpnEngine(vpnFd)
}

//export Java_ru_murasya_vpn_MyVpnService_stopGoCore
func Java_ru_murasya_vpn_MyVpnService_stopGoCore(env *C.JNIEnv, clazz C.jclass) {
	stopVpnEngineInternal()
	sendLog("GoCore engine stopped completely.")
}

func main() {}
