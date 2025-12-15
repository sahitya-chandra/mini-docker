package main

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: run|child")
		return
	}

	if arg := os.Args[1]; arg == "run" {
		run()
	} else if arg == "child" {
		child()
	} else {
		fmt.Print("Unknown")
	}
}

func run() {
	if len(os.Args) < 3 {
		fmt.Println("provide a cmd to run")
		return
	}

	cmd := exec.Command("/proc/self/exe", append([]string{"child"}, os.Args[2:]...)...)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Println("RUN PID =", os.Getpid())
	cmd.Run()
}

func child() {
	// cmd := os.Args[2]
	args := os.Args[2:]
	fmt.Println("Running inside child")

	syscall.Sethostname([]byte("container"))
	syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, "")
	syscall.Mount("proc", "/proc", "proc", 0, "")

	if !strings.HasPrefix(args[0], "/") {
		fmt.Println("Error: command must be an absolute path")
		os.Exit(1)
	}

	// syscall.Exec(cmd, args, os.Environ())
	pid, err := syscall.ForkExec(
		args[0],
		args,
		&syscall.ProcAttr{
			Files: []uintptr{
				os.Stdin.Fd(),
				os.Stdout.Fd(),
				os.Stderr.Fd(),
			},
		},
	)

	if err != nil {
		panic(err)
	}

	syscall.Setpgid(pid, pid)
	unix.IoctlSetInt(int(os.Stdin.Fd()), unix.TIOCSPGRP, pid)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

	fmt.Println("Init PID:", os.Getpid(), "Child PID:", pid)
	
	go func() {
		for sig := range sigCh {
			syscall.Kill(pid, sig.(syscall.Signal))
		}
	}()
		
	var ws syscall.WaitStatus
	syscall.Wait4(pid, &ws, 0, nil)

	unix.IoctlSetInt(int(os.Stdin.Fd()), unix.TIOCSPGRP, os.Getpid())
	os.Exit(ws.ExitStatus())

}