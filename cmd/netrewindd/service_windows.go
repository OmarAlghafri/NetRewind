package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/store"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

// serviceName is the Windows service the recorder registers itself as.
const serviceName = "netrewindd"

const serviceDisplayName = "NetRewind recorder"

const serviceDescription = "Records network state changes (interfaces, addresses, routes, neighbours) " +
	"to a local event store so an outage can be reconstructed after the fact. " +
	"Listens only on a local named pipe; never on the network."

// serviceLogMaxBytes bounds the service log file; above it the file is
// rotated once (netrewindd.log -> netrewindd.log.1). The record is the
// event store, not this log, so one generation of it is enough.
const serviceLogMaxBytes = 10 << 20

// serviceCommand handles "netrewindd service <install|uninstall|start|stop|status>"
// before any configuration is loaded, since installing needs no config and
// the config file may not exist yet. handled is false when args are not a
// service command at all.
func serviceCommand(args []string) (handled bool, err error) {
	if len(args) < 1 || args[0] != "service" {
		return false, nil
	}
	if len(args) < 2 {
		return true, errors.New("usage: netrewindd service install|uninstall|start|stop|status [--allow-user SID]")
	}
	switch args[1] {
	case "install":
		var allow []string
		for i := 2; i+1 < len(args); i += 2 {
			if args[i] == "--allow-user" {
				allow = append(allow, args[i+1])
			}
		}
		return true, installService(allow)
	case "uninstall":
		return true, uninstallService()
	case "start":
		return true, controlService(func(s *mgr.Service) error { return s.Start() })
	case "stop":
		return true, controlService(func(s *mgr.Service) error {
			_, err := s.Control(svc.Stop)
			return err
		})
	case "status":
		return true, controlService(func(s *mgr.Service) error {
			st, err := s.Query()
			if err != nil {
				return err
			}
			fmt.Printf("%s: %s (pid %d)\n", serviceName, stateName(st.State), st.ProcessId)
			return nil
		})
	}
	return true, fmt.Errorf("netrewindd service: unknown command %q", args[1])
}

// runService takes over when the process was started by the service control
// manager: it runs the recorder until the manager asks it to stop, with the
// log going to a file under the data directory since a service has no
// stderr anyone reads.
func runService(log *slog.Logger, cfg config) (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false, nil
	}
	logFile, err := openServiceLog()
	if err == nil {
		defer logFile.Close()
		log = slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: logLevel(cfg.LogLevel)}))
	}
	if elog, err := eventlog.Open(serviceName); err == nil {
		defer elog.Close()
		elog.Info(1, "netrewindd "+version+" starting")
		defer elog.Info(1, "netrewindd stopped")
	}
	return true, svc.Run(serviceName, &serviceHandler{log: log, cfg: cfg})
}

type serviceHandler struct {
	log *slog.Logger
	cfg config
}

// Execute implements svc.Handler: the recorder runs in the background while
// this loop answers the service control manager.
func (h *serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	status <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, h.log, h.cfg) }()

	status <- svc.Status{State: svc.Running, Accepts: accepted}
	for {
		select {
		case err := <-done:
			// The recorder ended on its own: an unusable store, an updater
			// hand-over. A non-zero exit code makes the manager's recovery
			// actions restart it.
			status <- svc.Status{State: svc.StopPending}
			if err != nil {
				h.log.Error("recorder stopped", "err", err)
				return true, 1
			}
			return false, 0
		case r := <-requests:
			switch r.Cmd {
			case svc.Interrogate:
				status <- r.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(15 * time.Second):
					h.log.Warn("recorder did not stop in time; exiting anyway")
				}
				return false, 0
			}
		}
	}
}

func openServiceLog() (*os.File, error) {
	dir := store.DataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "netrewindd.log")
	if info, err := os.Stat(path); err == nil && info.Size() > serviceLogMaxBytes {
		os.Remove(path + ".1")
		os.Rename(path, path+".1")
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

func logLevel(level string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return slog.LevelInfo
	}
	return l
}

// installService registers the service to start automatically, writes a
// starting configuration if none exists, and grants the installing user
// (plus any --allow-user SIDs) access to the API pipe. The service runs as
// LocalSystem so that the record continues while nobody is logged in.
func installService(allowUsers []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)

	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	allow := append([]string{sid}, allowUsers...)
	for _, s := range allow {
		if _, err := windows.StringToSid(s); err != nil {
			return fmt.Errorf("--allow-user %q is not a SID (S-1-5-21-...): %w", s, err)
		}
	}

	dataDir := store.DataDir()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dataDir, err)
	}
	configPath := filepath.Join(dataDir, "netrewindd.yaml")
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(configPath, []byte(defaultServiceConfig(exe, dataDir, allow)), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", configPath, err)
		}
		fmt.Println("wrote", configPath)
	} else {
		fmt.Println("keeping existing", configPath)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager (run as administrator): %w", err)
	}
	defer m.Disconnect()

	if s, err := m.OpenService(serviceName); err == nil {
		s.Close()
		return fmt.Errorf("service %s is already installed; run \"netrewindd service uninstall\" first", serviceName)
	}
	s, err := m.CreateService(serviceName, exe, mgr.Config{
		DisplayName:      serviceDisplayName,
		Description:      serviceDescription,
		StartType:        mgr.StartAutomatic,
		DelayedAutoStart: false,
	}, "--config", configPath)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()

	// The recorder must survive the conditions it is there to record:
	// restart on failure, promptly, without giving up.
	restart := mgr.RecoveryAction{Type: mgr.ServiceRestart, Delay: 5 * time.Second}
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{restart, restart, restart}, 86400); err != nil {
		fmt.Println("warning: could not set recovery actions:", err)
	}
	if err := eventlog.InstallAsEventCreate(serviceName, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		fmt.Println("warning: could not register the event log source:", err)
	}
	fmt.Printf("installed service %s (%s)\n", serviceName, exe)
	fmt.Printf("API pipe access: %s\n", strings.Join(allow, ", "))
	if err := s.Start(); err != nil {
		return fmt.Errorf("installed, but could not start: %w", err)
	}
	fmt.Println("started")
	return nil
}

// defaultServiceConfig is the configuration the installer writes: rules
// next to the binary, the store in the data directory, the API pipe open to
// the installing user.
func defaultServiceConfig(exe, dataDir string, allow []string) string {
	rulesDir := filepath.Join(filepath.Dir(exe), "rules")
	var b strings.Builder
	fmt.Fprintf(&b, "# NetRewind recorder configuration (written by \"netrewindd service install\").\n")
	fmt.Fprintf(&b, "# Every key is documented in the sample shipped with the package.\n")
	fmt.Fprintf(&b, "db: %s\n", yamlString(filepath.Join(dataDir, "events.db")))
	fmt.Fprintf(&b, "rules: %s\n", yamlString(rulesDir))
	fmt.Fprintf(&b, "retention: 168h\n")
	fmt.Fprintf(&b, "log_level: info\n")
	fmt.Fprintf(&b, "update:\n  check: true\n  apply: false\n")
	fmt.Fprintf(&b, "api:\n  enabled: true\n  allow_users:\n")
	for _, s := range allow {
		fmt.Fprintf(&b, "    - %s\n", s)
	}
	return b.String()
}

func yamlString(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}

func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager (run as administrator): %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %s is not installed", serviceName)
	}
	defer s.Close()
	if st, err := s.Query(); err == nil && st.State != svc.Stopped {
		if _, err := s.Control(svc.Stop); err == nil {
			for i := 0; i < 30; i++ {
				time.Sleep(500 * time.Millisecond)
				if st, err := s.Query(); err == nil && st.State == svc.Stopped {
					break
				}
			}
		}
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	eventlog.Remove(serviceName)
	fmt.Printf("uninstalled service %s; the event store and configuration under %s were left in place\n",
		serviceName, store.DataDir())
	return nil
}

func controlService(f func(*mgr.Service) error) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager (run as administrator): %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %s is not installed", serviceName)
	}
	defer s.Close()
	return f(s)
}

func stateName(s svc.State) string {
	switch s {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "starting"
	case svc.StopPending:
		return "stopping"
	case svc.Running:
		return "running"
	case svc.ContinuePending, svc.PausePending, svc.Paused:
		return "paused"
	}
	return fmt.Sprintf("state(%d)", s)
}

func currentUserSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("determine the current user: %w", err)
	}
	return user.User.Sid.String(), nil
}

var _ io.Writer = (*os.File)(nil)
