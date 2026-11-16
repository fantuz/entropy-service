package main

import (
	"context"
	"errors"
	"io"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

func nextServiceStatus(t *testing.T, statuses <-chan svc.Status, want svc.State) {
	t.Helper()

	select {
	case status := <-statuses:
		if status.State != want {
			t.Fatalf("SCM state %v, want %v", status, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SCM status was not delivered")
	}
}

func TestWindowsServiceJoinsStopAndShutdown(t *testing.T) {
	for _, command := range []svc.Cmd{svc.Stop, svc.Shutdown} {
		t.Run(strconv.Itoa(int(command)), func(t *testing.T) {
			requests := make(chan svc.ChangeRequest)
			statuses := make(chan svc.Status, 10)
			cancelled := make(chan struct{})
			release := make(chan struct{})
			finished := make(chan uint32, 1)

			service := windowsService{run: func(ctx context.Context, ready func()) error {
				ready()
				<-ctx.Done()
				close(cancelled)
				<-release

				return nil
			}}
			go func() { _, code := service.Execute(nil, requests, statuses); finished <- code }()

			nextServiceStatus(t, statuses, svc.StartPending)
			nextServiceStatus(t, statuses, svc.Running)

			requests <- svc.ChangeRequest{Cmd: svc.Interrogate}

			nextServiceStatus(t, statuses, svc.Running)

			requests <- svc.ChangeRequest{Cmd: command}

			nextServiceStatus(t, statuses, svc.StopPending)
			<-cancelled

			select {
			case <-finished:
				t.Fatal("SCM handler returned before the application joined")
			default:
			}

			close(release)

			select {
			case code := <-finished:
				if code != 0 {
					t.Fatalf("stop exit %d", code)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("SCM handler did not join shutdown")
			}
		})
	}
}

func TestWindowsServiceStartupFailure(t *testing.T) {
	statuses := make(chan svc.Status, 10)
	service := windowsService{run: func(context.Context, func()) error { return errors.New("startup refused") }}

	specific, code := service.Execute(nil, make(chan svc.ChangeRequest), statuses)
	if !specific || code != 1 {
		t.Fatalf("failure not reported to SCM: %t %d", specific, code)
	}

	nextServiceStatus(t, statuses, svc.StartPending)

	select {
	case status := <-statuses:
		t.Fatalf("failed startup reported another state: %v", status)
	default:
	}
}

func TestWindowsServiceStopDuringStartup(t *testing.T) {
	requests := make(chan svc.ChangeRequest)
	statuses := make(chan svc.Status, 10)
	done := make(chan struct{})

	service := windowsService{run: func(ctx context.Context, ready func()) error {
		<-ctx.Done()
		ready() // A late startup completion must never overwrite StopPending.

		return nil
	}}
	go func() { _, _ = service.Execute(nil, requests, statuses); close(done) }()

	nextServiceStatus(t, statuses, svc.StartPending)

	requests <- svc.ChangeRequest{Cmd: svc.Stop}

	nextServiceStatus(t, statuses, svc.StopPending)

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("startup cancellation did not join")
	}

	select {
	case status := <-statuses:
		t.Fatalf("late readiness was reported: %v", status)
	default:
	}
}

func TestWindowsServiceInvalidNames(t *testing.T) {
	for _, name := range []string{"", "../other", `NT SERVICE\othere`, "two names", "_invalid"} {
		if _, err := parseServiceOptions([]string{"-name=" + name}, io.Discard); err == nil {
			t.Errorf("accepted invalid service name %q", name)
		}
	}
}
