package main

import (
	"fmt"
	"os"
	"os/exec"
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
		Cloneflags: syscall.CLONE_NEWUTS,
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Println("RUN PID =", os.Getpid())
	cmd.Run()
}

func child() {
	cmd := os.Args[2]
	args := os.Args[2:]
	fmt.Println("CHILD PID =", os.Getpid())
	fmt.Println("Running inside child")


	syscall.Sethostname([]byte("container"))

	fmt.Println("CHILD PID =", os.Getpid())
	syscall.Exec(cmd, args, os.Environ())

}