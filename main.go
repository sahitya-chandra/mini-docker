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
		Cloneflags: 
			// syscall.CLONE_NEWUSER | 
			syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWUTS,
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Println("RUN PID =", os.Getpid())
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	cmd.Dir = cwd

	cmd.Run()
	
	// cmd.Start()
	// pid := cmd.Process.Pid
	// fmt.Println("Child PID:", pid)
	// fmt.Println("Host UID:", os.Getuid(), "Host GID:", os.Getgid())

	// uidMap := fmt.Sprintf("0 %d 1\n", os.Getuid())
	// err := os.WriteFile(fmt.Sprintf("/proc/%d/uid_map", pid), []byte(uidMap), 0)
	// if err != nil {
	// 	panic(err)
	// }

	// err = os.WriteFile(fmt.Sprintf("/proc/%d/setgroups", pid), []byte("deny"), 0)
	// if err != nil {
	// 	panic(err)
	// }

	// gidMap := fmt.Sprintf("0 %d 1\n", os.Getgid())
	// err = os.WriteFile(fmt.Sprintf("/proc/%d/gid_map", pid), []byte(gidMap), 0)
	// if err != nil {
	// 	panic(err)
	// }

	// cmd.Wait()
}

func child() {
	// cmd := os.Args[2]
	args := os.Args[2:]
	fmt.Println("Running inside child")

	syscall.Sethostname([]byte("container"))
	syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, "")
	// syscall.Mount("proc", "/proc", "proc", 0, "")

	if !strings.HasPrefix(args[0], "/") {
		fmt.Println("Error: command must be an absolute path")
		os.Exit(1)
	}

	ttyFD := int(os.Stdin.Fd())

	origPgrp, err := unix.IoctlGetInt(ttyFD, unix.TIOCGPGRP)
	if err != nil {
		panic(err)
	}

	newRoot := "./rootfs"
	oldRoot := "./rootfs/oldroot"

	// if err := os.Chdir(newRoot); err != nil {
	// 	panic(err)
	// }
	
	if err := syscall.Mount(newRoot, newRoot, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		panic("bind mount failed: " + err.Error())
	}

	if err := os.MkdirAll(oldRoot, 0700); err != nil {
		panic(err)
	}
	
	// pivot using relative paths
	if err := syscall.PivotRoot(newRoot, oldRoot); err != nil {
		panic("pivot_root failed: " + err.Error())
	}

	if err := os.Chdir("/"); err != nil {
		panic(err)
	}

	// syscall.Mount("proc", "/proc", "proc", 0, "")
	// syscall.Mount("sysfs", "/sys", "sysfs", 0, "")
	// syscall.Mount("tmpfs", "/dev", "tmpfs", 0, "")

	if err := syscall.Unmount("/oldroot", syscall.MNT_DETACH); err != nil {
		panic(err)
	}
	os.RemoveAll("/oldroot")

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

	syscall.Setsid()
	syscall.Setpgid(pid, pid)
	unix.IoctlSetInt(ttyFD, unix.TIOCSPGRP, pid)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

	fmt.Println("Init PID:", os.Getpid(), "Child PID:", pid)
	
	go func() {
		for sig := range sigCh {
			syscall.Kill(pid, sig.(syscall.Signal))
		}
	}()
		
	var exitCode int

	for {
		var ws syscall.WaitStatus
		wpid, err := syscall.Wait4(-1, &ws, 0, nil)
		if err != nil {
			break
		}

		if wpid == pid {
			if ws.Exited() {
				exitCode = ws.ExitStatus()
			} else if ws.Signaled() {
				exitCode = 128 + int(ws.Signal())
			}
			break
		}
	}

	unix.IoctlSetInt(ttyFD, unix.TIOCSPGRP, origPgrp)
	os.Exit(exitCode)

}