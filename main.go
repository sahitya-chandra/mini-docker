package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/creack/pty"
	"github.com/joho/godotenv"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func main() {
	godotenv.Load()
	if len(os.Args) < 2 {
		fmt.Println("Usage: run|child")
		return
	}

	if arg := os.Args[1]; arg == "run" {
		run()
	} else if arg == "child" {
		child()
	} else {
		fmt.Printf("Unknown argument: %s\n", arg)
	}
}

func run() {
	if len(os.Args) < 3 {
		fmt.Println("provide a cmd to run")
		return
	}

	cmd := exec.Command("/proc/self/exe", append([]string{"child"}, os.Args[2:]...)...)

	// Set up namespaces
	cloneFlags := uintptr(syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWUTS | syscall.CLONE_NEWIPC)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: cloneFlags,
	}

	if os.Getuid() != 0 {
		cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWUSER
		cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: os.Getuid(), Size: 1},
		}
		cmd.SysProcAttr.GidMappings = []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: os.Getgid(), Size: 1},
		}
	}

	if term.IsTerminal(int(os.Stdin.Fd())) {
		f, err := pty.Start(cmd)
		if err != nil {
			panic(fmt.Sprintf("failed to start with PTY: %v", err))
		}
		defer f.Close()

		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGWINCH)
		go func() {
			for range ch {
				if err := pty.InheritSize(os.Stdin, f); err != nil {
					fmt.Printf("error resizing pty: %s\n", err)
				}
			}
		}()
		ch <- syscall.SIGWINCH
		defer func() { signal.Stop(ch); close(ch) }()

		oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			panic(err)
		}
		defer term.Restore(int(os.Stdin.Fd()), oldState)

		go func() { io.Copy(f, os.Stdin) }()
		io.Copy(os.Stdout, f)

		cmd.Wait()
	} else {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			panic(err)
		}
		cmd.Wait()
	}
}

func child() {
	args := os.Args[2:]

	// Identify the host PTY slave path before we pivot
	// This helps the 'tty' command inside the container.
	hostPtyPath, _ := os.Readlink("/proc/self/fd/0")

	must(syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""))

	rootfs := os.Getenv("ROOTFS_PATH")
	if rootfs == "" {
		panic("ROOTFS_PATH not set")
	}
	absRootfs, err := filepath.Abs(rootfs)
	must(err)
	rootfs = absRootfs

	must(syscall.Mount(rootfs, rootfs, "", syscall.MS_BIND|syscall.MS_REC, ""))

	oldroot := filepath.Join(rootfs, "oldroot")
	must(os.MkdirAll(oldroot, 0700))

	must(syscall.PivotRoot(rootfs, oldroot))
	must(os.Chdir("/"))

	// Mount Pseudo-filesystems
	must(syscall.Mount("proc", "/proc", "proc", 0, ""))
	must(syscall.Mount("sysfs", "/sys", "sysfs", syscall.MS_RDONLY, ""))
	must(syscall.Mount("tmpfs", "/dev", "tmpfs", syscall.MS_NOSUID|syscall.MS_NOEXEC, "mode=755"))

	// Setup Device Nodes
	// If we have a TTY on the host, bind mount it to /dev/console
	if hostPtyPath != "" && strings.HasPrefix(hostPtyPath, "/dev/pts/") {
		// We must create the mount point first
		f, _ := os.Create("/dev/console")
		if f != nil {
			f.Close()
			// Use the oldroot path to access the host's PTY device
			fullHostPtyPath := filepath.Join("/oldroot", hostPtyPath)
			if err := syscall.Mount(fullHostPtyPath, "/dev/console", "", syscall.MS_BIND, ""); err != nil {
				// Fallback to mknod if bind mount fails
				must(syscall.Mknod("/dev/console", syscall.S_IFCHR|0600, int(unix.Mkdev(5, 1))))
			}
		}
	} else {
		must(syscall.Mknod("/dev/console", syscall.S_IFCHR|0600, int(unix.Mkdev(5, 1))))
	}

	must(syscall.Mknod("/dev/null", syscall.S_IFCHR|0666, int(unix.Mkdev(1, 3))))
	must(syscall.Mknod("/dev/zero", syscall.S_IFCHR|0666, int(unix.Mkdev(1, 5))))
	must(syscall.Mknod("/dev/full", syscall.S_IFCHR|0666, int(unix.Mkdev(1, 7))))
	must(syscall.Mknod("/dev/random", syscall.S_IFCHR|0666, int(unix.Mkdev(1, 8))))
	must(syscall.Mknod("/dev/urandom", syscall.S_IFCHR|0666, int(unix.Mkdev(1, 9))))

	// Link /dev/tty to /dev/console
	must(os.Symlink("/dev/console", "/dev/tty"))

	must(os.MkdirAll("/dev/pts", 0755))
	must(syscall.Mount("devpts", "/dev/pts", "devpts", 0, "newinstance,ptmxmode=0666,mode=0620"))
	must(os.Symlink("/dev/pts/ptmx", "/dev/ptmx"))

	must(os.Symlink("/proc/self/fd", "/dev/fd"))
	must(os.Symlink("/proc/self/fd/0", "/dev/stdin"))
	must(os.Symlink("/proc/self/fd/1", "/dev/stdout"))
	must(os.Symlink("/proc/self/fd/2", "/dev/stderr"))

	// 5. Setup Controlling Terminal
	var origPgrp int
	isTTY := term.IsTerminal(0)
	if isTTY {
		if pgrp, err := unix.IoctlGetInt(0, unix.TIOCGPGRP); err == nil {
			origPgrp = pgrp
		}
		// TIOCSCTTY: set controlling terminal
		if err := unix.IoctlSetInt(0, unix.TIOCSCTTY, 1); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to set TIOCSCTTY: %v\n", err)
		}
	}

	must(syscall.Unmount("/oldroot", syscall.MNT_DETACH))
	must(os.RemoveAll("/oldroot"))

	must(syscall.Sethostname([]byte("container")))

	cmdPath, err := exec.LookPath(args[0])
	if err != nil {
		fmt.Printf("Command not found: %s\n", args[0])
		os.Exit(1)
	}

	pid, err := syscall.ForkExec(
		cmdPath,
		args,
		&syscall.ProcAttr{
			Env:   os.Environ(),
			Files: []uintptr{0, 1, 2},
		},
	)
	must(err)

	syscall.Setpgid(pid, pid)
	if isTTY {
		_ = unix.IoctlSetInt(0, unix.TIOCSPGRP, pid)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

	go func() {
		for sig := range sigCh {
			_ = syscall.Kill(pid, sig.(syscall.Signal))
		}
	}()

	var exitCode int
	for {
		var ws syscall.WaitStatus
		wpid, err := syscall.Wait4(-1, &ws, 0, nil)
		if err != nil {
			if err == syscall.ECHILD {
				break
			}
			continue
		}
		if wpid == pid {
			if ws.Exited() {
				exitCode = ws.ExitStatus()
			} else if ws.Signaled() {
				exitCode = 128 + int(ws.Signal())
			}
		}
	}

	if isTTY && origPgrp != 0 {
		_ = unix.IoctlSetInt(0, unix.TIOCSPGRP, origPgrp)
	}

	os.Exit(exitCode)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
