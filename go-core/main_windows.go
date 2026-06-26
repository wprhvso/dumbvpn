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

	// Адрес VPN-сервера. Маршрут к нему всегда держим через физический шлюз,
	// иначе туннель замкнётся сам на себя.
	serverIP = "79.137.207.89"

	// Шлюз внутри туннеля (tungo поднимается с 10.0.0.2/24).
	tunGateway = "10.0.0.1"
	tunIface   = "tungo"
)

// Приватные диапазоны, которые должны ходить мимо туннеля, напрямую через
// физический шлюз. Аналог bypassCIDRs из main_linux.go, но в формате
// "адрес сети" + "маска" для команды route.
var bypassRoutes = []struct{ dest, mask string }{
	{"10.0.0.0", "255.0.0.0"},      // RFC1918
	{"172.16.0.0", "255.240.0.0"},  // RFC1918
	{"192.168.0.0", "255.255.0.0"}, // RFC1918
	{"100.64.0.0", "255.192.0.0"},  // CGNAT (Tailscale и т.п.)
	{"169.254.0.0", "255.255.0.0"}, // link-local
}

// elog — приёмник логов службы. Когда бинарь запущен SCM, пишем в Event Log;
// в интерактивном режиме (debug.Run) — туда же, но он дублирует в stdout.
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

Использование: %s <команда>

Команды:
  install     Установить службу с автозапуском (Automatic) и запустить её
  uninstall   Остановить и удалить службу
  run         Запустить VPN-движок (так стартует служба и автозапуск)

Командам install/uninstall и реальной работе требуются права администратора.
`, os.Args[0])
}

func main() {
	if len(os.Args) < 2 {
		// Запуск без аргументов: либо нас дёрнул SCM, либо пользователь
		// просто кликнул по exe. В первом случае работаем как служба.
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
			fmt.Printf("Установка не удалась: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Служба %q установлена и запущена (автозапуск включён).\n", serviceName)
	case "uninstall", "remove":
		if err := removeService(); err != nil {
			fmt.Printf("Удаление не удалось: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Служба %q удалена.\n", serviceName)
	case "run":
		runService()
	default:
		usage()
		os.Exit(2)
	}
}

// runService запускает обработчик службы. Если процесс поднят SCM —
// используем svc.Run; если запущен из консоли (отладка) — debug.Run,
// который дополнительно печатает логи в stdout.
func runService() {
	isService, err := svc.IsWindowsService()
	if err != nil {
		fmt.Printf("Не удалось определить режим запуска: %v\n", err)
		os.Exit(1)
	}

	if isService {
		el, err := eventlog.Open(serviceName)
		if err == nil {
			elog = el
			defer el.Close()
		}
		if err := svc.Run(serviceName, &vpnService{}); err != nil {
			windowsSendLog("Служба завершилась с ошибкой: %v", err)
			os.Exit(1)
		}
		return
	}

	// Интерактивный режим (для отладки): логи идут в консоль.
	elog = debug.New(serviceName)
	windowsSendLog("Запуск в интерактивном режиме (debug). Ctrl+C для остановки.")
	if err := debug.Run(serviceName, &vpnService{}); err != nil {
		windowsSendLog("Отладочный запуск завершился с ошибкой: %v", err)
		os.Exit(1)
	}
}

// vpnService реализует svc.Handler — основной жизненный цикл службы.
type vpnService struct{}

func (m *vpnService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	changes <- svc.Status{State: svc.StartPending}

	windowsSendLog("Служба запускается. Инициализация сети и VPN-движка...")
	setupWindowsNetwork()
	go startVpnEngine(0)

	changes <- svc.Status{State: svc.Running, Accepts: accepted}
	windowsSendLog("Служба запущена.")

loop:
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			windowsSendLog("Получена команда остановки (%v). Завершаем работу...", c.Cmd)
			break loop
		default:
			windowsSendLog("Неожиданная управляющая команда: %d", c.Cmd)
		}
	}

	changes <- svc.Status{State: svc.StopPending}
	stopVpnEngineInternal()
	cleanupWindowsNetwork()
	changes <- svc.Status{State: svc.Stopped}
	windowsSendLog("Служба остановлена.")
	return false, 0
}

func installService() error {
	exepath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("не удалось определить путь к исполняемому файлу: %w", err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("не удалось подключиться к менеджеру служб (нужны права администратора?): %w", err)
	}
	defer m.Disconnect()

	if s, err := m.OpenService(serviceName); err == nil {
		s.Close()
		return fmt.Errorf("служба %q уже установлена", serviceName)
	}

	s, err := m.CreateService(serviceName, exepath, mgr.Config{
		DisplayName:  displayName,
		Description:  description,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
	}, "run")
	if err != nil {
		return fmt.Errorf("не удалось создать службу: %w", err)
	}
	defer s.Close()

	// Регистрируем источник в Event Log (best-effort).
	if err := eventlog.InstallAsEventCreate(serviceName, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil {
		// Источник может уже существовать — это не критично.
		fmt.Printf("Предупреждение: не удалось зарегистрировать источник Event Log: %v\n", err)
	}

	if err := s.Start(); err != nil {
		return fmt.Errorf("служба создана, но не удалось её запустить: %w", err)
	}

	return nil
}

func removeService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("не удалось подключиться к менеджеру служб (нужны права администратора?): %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("служба %q не установлена: %w", serviceName, err)
	}
	defer s.Close()

	// Пытаемся остановить (best-effort) и дождаться завершения.
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
		return fmt.Errorf("не удалось удалить службу: %w", err)
	}

	if err := eventlog.Remove(serviceName); err != nil {
		fmt.Printf("Предупреждение: не удалось удалить источник Event Log: %v\n", err)
	}

	return nil
}

// runCmd — аналог runCmd из main_linux.go, выполняет внешнюю команду и
// логирует её вывод при ошибке.
func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		windowsSendLog("Команда %q %v завершилась с ошибкой: %v (%s)", name, args, err, strings.TrimSpace(string(out)))
	}
	return err
}

// detectGateway определяет физический шлюз по умолчанию, парся вывод
// `route print -4`: строка дефолтного маршрута имеет вид
// "0.0.0.0  0.0.0.0  <шлюз>  <интерфейс>  <метрика>".
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
	return "", fmt.Errorf("не удалось найти шлюз по умолчанию в выводе route print")
}

func setupWindowsNetwork() {
	gateway, err := detectGateway()
	if err != nil {
		windowsSendLog("CRITICAL: не удалось определить физический шлюз: %v", err)
		return
	}
	windowsSendLog("Обнаружен физический шлюз: %s", gateway)

	// Маршрут к VPN-серверу — всегда напрямую через физический шлюз.
	runCmd("route", "delete", serverIP)
	if err := runCmd("route", "add", serverIP, "mask", "255.255.255.255", gateway); err == nil {
		windowsSendLog("Маршрут к серверу %s закреплён через %s", serverIP, gateway)
	}

	// Bypass приватных диапазонов — напрямую через физический шлюз.
	for _, b := range bypassRoutes {
		runCmd("route", "delete", b.dest)
		if err := runCmd("route", "add", b.dest, "mask", b.mask, gateway); err == nil {
			windowsSendLog("Bypass-маршрут добавлен: %s/%s -> %s", b.dest, b.mask, gateway)
		}
	}

	// Глобальную маршрутизацию включаем после появления интерфейса tungo.
	go func() {
		windowsSendLog("Ожидание создания интерфейса %s движком GOST/wintun...", tunIface)
		var idx int
		for {
			if ifc, err := net.InterfaceByName(tunIface); err == nil {
				idx = ifc.Index
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		windowsSendLog("Интерфейс %s обнаружен (index %d)! Включаем глобальную маршрутизацию...", tunIface, idx)

		ifStr := fmt.Sprintf("%d", idx)
		// Дефолт двумя половинами /1, чтобы перебить системный 0.0.0.0/0,
		// но не затирать его (как и на Linux).
		runCmd("route", "add", "0.0.0.0", "mask", "128.0.0.0", tunGateway, "if", ifStr, "metric", "1")
		runCmd("route", "add", "128.0.0.0", "mask", "128.0.0.0", tunGateway, "if", ifStr, "metric", "1")

		// DNS направляем в туннель.
		runCmd("netsh", "interface", "ipv4", "set", "dns", "name="+tunIface, "static", tunGateway)

		windowsSendLog("VPN полностью поднят и маршрутизирован. Enjoy!")
	}()
}

func cleanupWindowsNetwork() {
	windowsSendLog("Очистка сетевых маршрутов...")
	runCmd("route", "delete", "0.0.0.0", "mask", "128.0.0.0")
	runCmd("route", "delete", "128.0.0.0", "mask", "128.0.0.0")
	runCmd("route", "delete", serverIP)
	for _, b := range bypassRoutes {
		runCmd("route", "delete", b.dest)
	}
	runCmd("netsh", "interface", "ipv4", "set", "dns", "name="+tunIface, "dhcp")
	windowsSendLog("Очистка завершена.")
}
