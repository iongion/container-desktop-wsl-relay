package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	distribution     string
	socketPath       string
	namedPipe        string
	relayProgramPath string
	pidFile          string
	bufferSize       int
)

var signalChan chan (os.Signal) = make(chan os.Signal, 1)
var cmd *exec.Cmd
var stdinPipe io.WriteCloser
var stdoutPipe io.ReadCloser
var conn net.Conn
var closed = false

func init() {
	flag.StringVar(&distribution, "distribution", os.Getenv(("WSL_DISTRO_NAME")), "WSL Distribution name")
	flag.StringVar(&socketPath, "socket", "/var/run/docker.sock", "Container engine socket path")
	flag.StringVar(&namedPipe, "pipe", "\\\\.\\pipe\\container-desktop", "Named pipe to relay through")
	flag.StringVar(&relayProgramPath, "relay-program-path", "container-desktop-wsl-relay.exe", "Named pipe relay program path")
	flag.StringVar(&pidFile, "pid-file", "", "PID file path")
	flag.Usage = func() {
		flag.PrintDefaults()
	}
	log.SetOutput(os.Stderr)
	log.SetPrefix("[linux]")
}

func handleConnection(conn net.Conn, stdin io.WriteCloser, stdout io.ReadCloser) {
	defer conn.Close()

	go func() {
		if closed {
			log.Println("Connection closed")
			return
		}
		log.Println("Reading from stdout and sending to socket")
		// Send data from Windows executable's stdout back to Unix socket
		_, err := io.Copy(conn, stdout) // Copy stdout of the process back to the socket (stream)
		if err != nil {
			log.Printf("Error reading from stdout: %v", err)
		}
	}()

	if closed {
		log.Println("Connection closed")
		return
	}
	log.Println("Writing socket to stdin")
	_, err := io.Copy(stdin, conn) // Copy socket data to the process stdin (stream)
	if err != nil {
		log.Printf("Error writing to stdin: %v", err)
	}
}

func main() {
	flag.Parse()

	if len(socketPath) == 0 {
		log.Fatalln("Socket path is blank/empty")
	}

	if len(namedPipe) == 0 {
		log.Fatalln("Named pipe is blank/empty")
	}

	if len(relayProgramPath) == 0 {
		log.Fatalln("Relay program path is blank/empty")
	}

	if _, err := os.Stat(relayProgramPath); errors.Is(err, os.ErrNotExist) {
		log.Fatalf(
			"%v relay program not found in %s",
			err,
			relayProgramPath,
		)
	}

	if len(pidFile) > 0 {
		log.Printf("Writing main PID %d to %s\n", os.Getpid(), pidFile)
		pidContents := []byte(strconv.FormatInt(int64(os.Getpid()), 10))
		err := os.WriteFile(pidFile, pidContents, 0644)
		if err != nil {
			panic(err)
		}
	}

	defer close(signalChan)

	signal.Notify(signalChan,
		os.Interrupt,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT,
		syscall.SIGSEGV)

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	// Ctrl+C handler
	go func() {
		<-signalChan
		signal.Stop(signalChan)
		cancelFunc()
		log.Println("Exit trapped - closing connection")
		cmd.Process.Kill()
		syscall.Kill(cmd.Process.Pid, syscall.SIGTERM)
		log.Println("Command killed")
		/*
			pgid, err := syscall.Getpgid(cmd.Process.Pid)
			if err == nil {
				err = syscall.Kill(-pgid, syscall.SIGINT)
				if err != nil {
					log.Printf("Failed to kill process group: %v", err)
				}
			} else {
				log.Printf("Failed to get process group ID: %v", err)
			}
		*/
		os.Exit(0)
	}()

	// Create a Unix domain socket connection
retry:
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		log.Printf("Failed to create Unix domain socket: %v\n", err)
		time.Sleep(2 * time.Second)
		log.Print("Retrying...")
		goto retry
	}
	defer conn.Close()

	// Start the Windows executable as a subprocess (once)
	log.Printf("Starting Windows relay executable: %s --pipe=%s --distribution=%s --parent-pid=%s", relayProgramPath, namedPipe, distribution, strconv.Itoa(os.Getpid()))
	// cmd = exec.Command(relayProgramPath, "--pipe", namedPipe)
	cmd := exec.CommandContext(ctx, relayProgramPath, "--pipe", namedPipe, "--distribution", distribution, "--parent-pid", strconv.Itoa(os.Getpid()))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: 0}
	cmd.Stderr = os.Stderr

	// Redirect stdin and stdout to the subprocess (single connection)
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		log.Fatalf("Failed to create stdin pipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("Failed to create stdout pipe: %v", err)
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start Windows native STD relay executable: %v", err)
	}

	log.Printf("Started Windows relay executable: %s PID: %d", relayProgramPath, cmd.Process.Pid)
	// Write the PID of the relay process to a file
	if len(pidFile) > 0 {
		relayPidFile := strings.Replace(pidFile, ".pid", "-relay.pid", 1)
		log.Printf("Writing relay PID %d to %s\n", cmd.Process.Pid, relayPidFile)
		pidContents := []byte(strconv.FormatInt(int64(cmd.Process.Pid), 10))
		err := os.WriteFile(relayPidFile, pidContents, 0644)
		if err != nil {
			panic(err)
		}
	}

	// Handle connection in a new goroutine
	go handleConnection(conn, stdinPipe, stdoutPipe)

	log.Printf("Waiting for connections and listening on %s...\n", namedPipe)
	// Wait for the process to finish
	if err := cmd.Wait(); err != nil {
		log.Printf("Windows executable exited with error: %v", err)
	}
	closed = true
	log.Println("Linux executable exited")
	os.Exit(0)
}
