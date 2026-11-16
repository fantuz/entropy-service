package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/transport"
	"github.com/fantuz/entropy-service/internal/privatefile"
)

const (
	serviceTimeout = 35 * time.Second
	serviceInstall = "install"
	serviceRun     = "run"
	serviceHelp    = "-help"
)

type serviceOptions struct {
	name       string
	directory  string
	selfSigned bool
	hosts      string
	serverArgs []string
}

func parseServiceOptions(args []string, output io.Writer) (serviceOptions, error) {
	opts := serviceOptions{}
	flags := flag.NewFlagSet("entropy-server service", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&opts.name, "name", "EntropyServer", "Windows service name")
	flags.StringVar(&opts.directory, "state-dir", filepath.Join(os.Getenv("ProgramData"), "entropy-service"), "Protected absolute state directory (install/run only)")
	flags.BoolVar(&opts.selfSigned, "self-signed", false, "Explicitly provision/reuse a self-signed identity as the service account; never renew or replace an existing pair")
	flags.StringVar(&opts.hosts, "cert-hosts", "localhost,127.0.0.1,::1", "Self-signed certificate SANs (install/run only)")

	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: entropy-server service install|start|stop|status|uninstall [options] [-- server-options]")
		fmt.Fprintln(output, "Use an elevated terminal and a permanently installed executable in an administrator-controlled directory.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return opts, err
	}

	if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`).MatchString(opts.name) {
		return opts, errors.New("service name must contain 1..64 ASCII letters, digits, underscores or hyphens, starting with a letter")
	}

	opts.serverArgs = flags.Args()

	return opts, nil
}

func runServiceCommand(args []string, output, diagnostics io.Writer) int {
	if len(args) == 0 {
		args = []string{serviceHelp}
	}

	operation := args[0]
	if operation == serviceHelp || operation == "--help" {
		_, _ = parseServiceOptions([]string{serviceHelp}, output)
		return 0
	}

	if operation != serviceInstall && operation != serviceRun && operation != "start" && operation != "stop" && operation != "status" && operation != "uninstall" {
		fmt.Fprintln(diagnostics, "unknown service operation; use service -help")
		return 2
	}

	opts, err := parseServiceOptions(args[1:], diagnostics)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}

	if err != nil {
		fmt.Fprintln(diagnostics, err)
		return 2
	}

	if operation == serviceRun || operation == serviceInstall {
		err = configureService(operation, opts, output)
	} else {
		// Do not silently ignore startup settings supplied to a control command.
		for _, arg := range args[1:] {
			if !strings.HasPrefix(arg, "-name=") {
				err = errors.New("control commands accept only -name=NAME; startup options belong to service install")
				break
			}
		}

		if err == nil {
			err = controlService(operation, opts.name, output)
		}
	}

	if err != nil {
		fmt.Fprintf(diagnostics, "Windows service %s failed: %v\n", operation, err)
		return 1
	}

	return 0
}

func configureService(operation string, opts serviceOptions, output io.Writer) error {
	if !filepath.IsAbs(opts.directory) || strings.HasPrefix(opts.directory, `\\`) {
		return errors.New("state-dir must be an absolute local path in an operator-controlled parent directory")
	}

	args := opts.serverArgs
	if operation == serviceInstall {
		args = append([]string{"-http=127.0.0.1:8080", "-https=127.0.0.1:8443"}, args...)
	}

	cfg, err := config.ParseArguments(args, output)
	if err != nil {
		return err
	}

	if err := cfg.ValidatePlatform(); err != nil {
		return err
	}

	if cfg.DescribeJSON || (opts.selfSigned && !cfg.EnableHTTPS) {
		return errors.New("service mode requires a running server; describe-json or self-signed with HTTPS disabled is invalid")
	}

	if operation == serviceRun {
		isService, err := svc.IsWindowsService()
		if err != nil {
			return err
		}

		if !isService {
			return errors.New("service run is reserved for the Windows Service Control Manager; use service start")
		}

		return svc.Run(opts.name, &windowsService{run: func(ctx context.Context, ready func()) error {
			return runServiceServer(ctx, cfg, opts, ready)
		}})
	}

	executable, err := os.Executable()
	if err != nil {
		return err
	}

	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM (use an elevated terminal): %w", err)
	}
	defer func() { _ = manager.Disconnect() }()

	serviceArgs := make([]string, 0, 7+len(args))
	serviceArgs = append(serviceArgs, serviceCommand, serviceRun, "-name="+opts.name, "-state-dir="+filepath.Clean(opts.directory), fmt.Sprintf("-self-signed=%t", opts.selfSigned), "-cert-hosts="+opts.hosts, "--")
	serviceArgs = append(serviceArgs, args...)

	service, err := manager.CreateService(opts.name, executable, mgr.Config{
		DisplayName:      "Entropy Server (" + opts.name + ")",
		Description:      "Entropy Service HTTP/WebSocket server; /ready reports generation freshness separately from SCM process state.",
		StartType:        mgr.StartAutomatic,
		DelayedAutoStart: true,
		ErrorControl:     mgr.ErrorNormal,
		ServiceStartName: `NT SERVICE\` + opts.name,
		SidType:          windows.SERVICE_SID_TYPE_UNRESTRICTED,
	}, serviceArgs...)
	if err != nil {
		return err
	}
	defer service.Close()

	sid, _, _, err := windows.LookupSID("", `NT SERVICE\`+opts.name)
	if err == nil {
		err = prepareServiceDirectory(opts.directory, sid)
	}

	if err != nil {
		return errors.Join(err, service.Delete())
	}

	fmt.Fprintf(output, "Installed %s (automatic, delayed start) as NT SERVICE\\%s.\nState: %s\nStart explicitly with: entropy-server service start -name=%s\n", opts.name, opts.name, opts.directory, opts.name)

	return nil
}

func controlService(operation, name string, output io.Writer) error {
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM (use an elevated terminal): %w", err)
	}
	defer func() { _ = manager.Disconnect() }()

	service, err := manager.OpenService(name)
	if err != nil {
		return err
	}
	defer service.Close()

	settings, err := service.Config()
	if err != nil {
		return err
	}

	command, err := windows.DecomposeCommandLine(settings.BinaryPathName)
	if err != nil {
		return err
	}

	executable, err := os.Executable()
	if err != nil {
		return err
	}

	if len(command) < 4 || !strings.EqualFold(filepath.Clean(command[0]), filepath.Clean(executable)) || command[1] != serviceCommand || command[2] != serviceRun || command[3] != "-name="+name || !strings.EqualFold(settings.ServiceStartName, `NT SERVICE\`+name) {
		return errors.New("refusing to control a service not installed by this entropy-server executable")
	}

	status, err := service.Query()
	if err != nil {
		return err
	}

	switch operation {
	case "status":
		state := map[svc.State]string{svc.Stopped: "stopped", svc.StartPending: "starting", svc.StopPending: "stopping", svc.Running: "running"}[status.State]
		if state == "" {
			state = fmt.Sprintf("unknown (%d)", status.State)
		}

		fmt.Fprintf(output, "Service: %s\nState: %s\nPID: %d\nAccount: %s\nCommand: %s\nExit: %d / %d\nSCM running is process/listener state; check /ready for seed freshness.\n", name, state, status.ProcessId, settings.ServiceStartName, settings.BinaryPathName, status.Win32ExitCode, status.ServiceSpecificExitCode)

		return nil
	case "start":
		if status.State == svc.Running {
			return nil
		}

		if err := service.Start(); err != nil {
			return err
		}

		return waitServiceState(service, svc.Running)
	case "stop":
		return stopService(service, status)
	case "uninstall":
		if status.State != svc.Stopped {
			return errors.New("stop the service before uninstalling; state, certificates and logs are always retained")
		}

		return service.Delete()
	default:
		return errors.New("unsupported service control operation")
	}
}

func stopService(service *mgr.Service, status svc.Status) error {
	if status.State == svc.Stopped {
		return nil
	}

	if status.State != svc.StopPending {
		if _, err := service.Control(svc.Stop); err != nil {
			return err
		}
	}

	return waitServiceState(service, svc.Stopped)
}

func waitServiceState(service *mgr.Service, wanted svc.State) error {
	deadline := time.NewTimer(serviceTimeout)
	defer deadline.Stop()

	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	for {
		status, err := service.Query()
		if err != nil {
			return err
		}

		if status.State == wanted {
			return nil
		}

		if wanted == svc.Running && status.State == svc.Stopped {
			return fmt.Errorf("service stopped during startup (exit %d / %d); inspect state-directory logs", status.Win32ExitCode, status.ServiceSpecificExitCode)
		}

		select {
		case <-deadline.C:
			return errors.New("timed out waiting for SCM; no process was killed, inspect service status and logs")
		case <-tick.C:
		}
	}
}

type windowsService struct {
	run func(context.Context, func()) error
}

func (service *windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	status := svc.Status{State: svc.StartPending, CheckPoint: 1, WaitHint: 10000}
	changes <- status

	ready := make(chan struct{})
	done := make(chan error, 1)
	readySignal := ready

	go func() { done <- service.run(ctx, func() { close(readySignal) }) }()

	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for {
		select {
		case err := <-done:
			if err != nil {
				log.Printf("Windows service failed: %v", err)
				return true, 1
			}

			return false, 0
		case <-ready:
			ready = nil

			status = svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
			changes <- status
		case request, ok := <-requests:
			if !ok || request.Cmd == svc.Stop || request.Cmd == svc.Shutdown {
				ready = nil
				requests = nil

				status = svc.Status{State: svc.StopPending, CheckPoint: 1, WaitHint: 10000}
				changes <- status

				cancel()
			} else if request.Cmd == svc.Interrogate {
				changes <- status
			}
		case <-tick.C:
			if status.State == svc.StartPending || status.State == svc.StopPending {
				status.CheckPoint++
				changes <- status
			}
		}
	}
}

func runServiceServer(ctx context.Context, cfg *config.Config, opts serviceOptions, ready func()) (result error) {
	sid, _, _, err := windows.LookupSID("", `NT SERVICE\`+opts.name)
	if err != nil {
		return err
	}

	if err := checkServiceDirectory(opts.directory, sid); err != nil {
		return err
	}

	if err := os.Chdir(opts.directory); err != nil {
		return err
	}

	file, err := privatefile.Create(fmt.Sprintf("server-%s-%d.log", time.Now().UTC().Format("20060102T150405.000000000Z"), os.Getpid()))
	if err != nil {
		return err
	}
	defer file.Close()

	if err := allowServicePublicFile(file.Name(), sid); err != nil {
		return err
	}

	os.Stdout, os.Stderr = file, file
	log.SetOutput(file)

	defer func() {
		if result != nil {
			log.Printf("Windows service failed: %v", result)
		}
	}()

	log.Printf("Windows service %s starting; state %s", opts.name, opts.directory)

	if opts.selfSigned {
		if _, err := transport.GenerateCertificate(transport.CertificateOptions{
			CertFile: cfg.CertFile, KeyFile: cfg.KeyFile, Hosts: strings.Split(opts.hosts, ","),
			ValidFor: 7 * 24 * time.Hour, IfMissing: true,
		}); err != nil {
			log.Printf("Service certificate provisioning: %v", err)
			return err
		}

		if err := allowServicePublicFile(cfg.CertFile, sid); err != nil {
			return err
		}
	}

	return runServer(ctx, cfg, ready)
}
