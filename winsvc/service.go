// Package winsvc runs the exe as a Windows service: it owns the tunnel, while the window stays an
// ordinary user process and talks to it over the named pipe.
package winsvc

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/alexus282/ByeByeRKN/windows/control"
	"github.com/alexus282/ByeByeRKN/windows/store"
)

// Run starts the service host. The SCM launches the exe with --service; a developer can run the
// same command in a console to watch it work without installing anything.
func Run() error {
	running, err := svc.IsWindowsService()
	if err != nil || !running {
		return runConsole()
	}
	return svc.Run(control.ServiceName, &handler{})
}

type handler struct{}

func (h *handler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	srv, err := start()
	if err != nil {
		logf("не удалось запустить службу: %v", err)
		return false, 1
	}
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-srv.stopped:
			logf("служба остановлена: %v", err)
			srv.server.Stop()
			return false, 1
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				changes <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				srv.server.Stop()
				return false, 0
			}
		}
	}
}

type running struct {
	server  *control.Server
	stopped chan error
}

func start() (*running, error) {
	path, err := store.MachinePath()
	if err != nil {
		return nil, err
	}
	st, err := store.Open(path)
	if err != nil {
		return nil, fmt.Errorf("настройки %s: %w", path, err)
	}
	server := control.NewServer(st)
	stopped := make(chan error, 1)
	go func() { stopped <- server.ListenAndServe() }()
	select {
	case err := <-stopped:
		return nil, err
	case <-time.After(300 * time.Millisecond):
		// The pipe is up: a failure after this moment is reported to the SCM as a stop.
	}
	return &running{server: server, stopped: stopped}, nil
}

func runConsole() error {
	r, err := start()
	if err != nil {
		return err
	}
	fmt.Println("ByeByeRKN service is listening on", control.PipeName)
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	select {
	case <-interrupt:
		r.server.Stop()
		return nil
	case err := <-r.stopped:
		return err
	}
}

// logf writes what the service did before a window could connect. The SCM keeps the service's own
// start errors, but route and engine failures are easier to read here.
func logf(format string, args ...any) {
	dir := os.Getenv("ProgramData")
	if dir == "" {
		return
	}
	dir = filepath.Join(dir, "ByeByeRKN")
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(dir, "service-error.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintf(file, "%s  %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}
