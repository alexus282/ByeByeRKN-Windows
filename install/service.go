package install

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/alexus282/ByeByeRKN/windows/control"
)

// removeService stops and deletes the service. A missing service is not an error: install and
// uninstall both start from whatever state the machine is in.
func removeService() error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(control.ServiceName)
	if err != nil {
		return nil
	}
	defer service.Close()
	if status, err := service.Query(); err == nil && status.State != svc.Stopped {
		service.Control(svc.Stop)
		waitStopped(service, 15*time.Second)
	}
	if err := service.Delete(); err != nil {
		return err
	}
	// The SCM keeps the name reserved until every handle is closed.
	for range 50 {
		if probe, err := manager.OpenService(control.ServiceName); err != nil {
			return nil
		} else {
			probe.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("служба %s всё ещё удаляется", control.ServiceName)
}

func createService(exe string) error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.CreateService(control.ServiceName, exe, mgr.Config{
		DisplayName:  control.ServiceTitle,
		Description:  "Туннель Cloudflare WARP на AmneziaWG. Окно ByeByeRKN управляет им без прав администратора.",
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
	}, "--service")
	if err != nil {
		return fmt.Errorf("создать службу: %w", err)
	}
	defer service.Close()
	// A crash must not leave the machine without its tunnel management.
	service.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 5 * time.Second}}, 3600)
	return nil
}

func startService() error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(control.ServiceName)
	if err != nil {
		return err
	}
	defer service.Close()
	if status, err := service.Query(); err == nil && status.State == svc.Running {
		return nil
	}
	if err := service.Start(); err != nil {
		return fmt.Errorf("запустить службу: %w", err)
	}
	return waitRunning(service, 20*time.Second)
}

func waitRunning(service *mgr.Service, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := service.Query()
		if err != nil {
			return err
		}
		switch status.State {
		case svc.Running:
			return nil
		case svc.Stopped:
			return fmt.Errorf("служба остановилась при запуске (код %d)", status.Win32ExitCode)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("служба не запустилась за %s", timeout)
}

func waitStopped(service *mgr.Service, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := service.Query()
		if err != nil || status.State == svc.Stopped {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
