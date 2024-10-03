//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/keybase/go-ps"
)

// See https://github.com/docker/go-plugins-helpers/blob/main/sdk/windows_listener.go
const (
	IO_BUFFER_SIZE = 32 * 1024 // 32KB
	// AllowEveryone grants full access permissions for everyone.
	AllowEveryone = "S:(ML;;NW;;;LW)D:(A;;0x12019f;;;WD)"
	// AllowCurrentUser grants full access permissions for the current user.
	AllowCurrentUser = "D:P(A;;GA;;;$SID)"
	// AllowServiceSystemAdmin grants full access permissions for Service, System, Administrator group and account.
	AllowServiceSystemAdmin = "D:(A;ID;FA;;;SY)(A;ID;FA;;;BA)(A;ID;FA;;;LA)(A;ID;FA;;;LS)"
)

var (
	distribution        string
	parentPid           int
	namedPipe           string
	permissions         string
	bufferSize          int
	pidFile             string
	pollInterval        = 2
	unixSocket          string
	relayProgramPath    string
	relayProgramOptions string
)

var signalChan chan (os.Signal) = make(chan os.Signal, 1)

func isProcessRunning(pid int) bool {
	// See https://github.com/golang/go/issues/33814
	// For testing shutdown use: powershell.exe -Command Start-Process -FilePath container-desktop-wsl-relay.exe -Wait
	process, err := ps.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		// log.Printf("Checking for running process ID %d - %v\n", pid, process)
		if process != nil {
			return true
		}
	}
	return false
}

func watchParentProcess(parentPid int) {
	for {
		if parentPid > 0 {
			if !isProcessRunning(parentPid) {
				log.Printf("Parent process ID %d is no longer running - shutting down\n", parentPid)
				signalChan <- syscall.SIGINT
				return
			}
		} else {
			log.Println("Parent process ID is not provided yet")
		}
		time.Sleep(time.Duration(pollInterval) * time.Second)
	}
}

func init() {
	flag.StringVar(&distribution, "distribution", os.Getenv("WSL_DISTRO_NAME"), "WSL Distribution name of the parent process")
	flag.IntVar(&parentPid, "parent-pid", -1, "Parent WSL Distribution process ID")
	flag.IntVar(&pollInterval, "poll-interval", 2, "Parent process polling interval in seconds - default is 2 seconds")
	flag.StringVar(&namedPipe, "named-pipe", "\\\\.\\pipe\\container-desktop", "Named pipe to relay through")
	flag.StringVar(&unixSocket, "unix-socket", "/var/run/docker.sock", "The Unix socket to relay through")
	flag.StringVar(&permissions, "permissions", "AllowCurrentUser", fmt.Sprintf("Named pipe permissions specifier - see https://learn.microsoft.com/en-us/windows/win32/ipc/named-pipe-security-and-access-rights\nAvailable are:\n\tAllowServiceSystemAdmin=%s\n\tAllowCurrentUser=%s\n\tAllowEveryone=%s\n", AllowServiceSystemAdmin, AllowCurrentUser, AllowEveryone))
	flag.IntVar(&bufferSize, "buffer-size", IO_BUFFER_SIZE, "I/O buffer size in bytes")
	flag.StringVar(&pidFile, "pid-file", "", "PID file path - The native Windows path where the native Windows PID is to be written")
	flag.StringVar(&relayProgramPath, "relay-program-path", "./socat-static", "The path to the WSL relay program")
	flag.StringVar(&relayProgramOptions, "relay-program-options", "", "The options to pass to the WSL relay program(socat UNIX-CONNECT options)")
	flag.Usage = func() {
		flag.PrintDefaults()
	}
	log.SetPrefix("[windows]")
	log.SetOutput(os.Stderr)
	// logFile, err := os.OpenFile("container-desktop-wsl-relay.exe.log", os.O_CREATE|os.O_APPEND|os.O_RDWR, 0666)
	// if err != nil {
	// 	panic(err)
	// }
	// mw := io.MultiWriter(os.Stderr, logFile)
	// log.SetOutput(mw)
}

func readWithDeadline(pipe io.Reader, buffer []byte, timeout time.Duration) (int, error) {
	readCh := make(chan struct {
		n   int
		err error
	})

	// Run the pipe reading in a goroutine to be able to timeout
	go func() {
		n, err := pipe.Read(buffer)
		readCh <- struct {
			n   int
			err error
		}{n, err}
	}()

	select {
	case result := <-readCh:
		return result.n, result.err
	case <-time.After(timeout):
		return 0, fmt.Errorf("Timeout")
	}
}

func handleClient(conn net.Conn, cmdArgs []string, ctx context.Context) {
	defer conn.Close()

	log.Printf("Client connected [%s]\n", conn.RemoteAddr().Network())

	log.Println("Spawning WSL relay executable: wsl.exe ", strings.Join(cmdArgs, " "))
	cmd := exec.CommandContext(ctx, "wsl.exe", cmdArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	cmd.Stderr = os.Stderr

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		log.Fatalf("Failed to create stdin pipe: %v", err)
	}
	defer stdinPipe.Close()

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("Failed to create stdout pipe: %v", err)
	}
	defer stdoutPipe.Close()

	// Start the process
	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start Windows native STD relay executable: %v", err)
	} else {
		log.Printf("Started Windows native STD relay executable with PID %d\n", cmd.Process.Pid)
	}

	err = conn.SetReadDeadline(time.Now().Add(1000 * time.Millisecond))
	if err != nil {
		log.Println("Error setting read deadline:", err)
		return
	}

	err = conn.SetWriteDeadline(time.Now().Add(5000 * time.Millisecond))
	if err != nil {
		log.Println("Error setting read deadline:", err)
		return
	}

	// Request - Read from connection and write to stdin pipe
	// Reading from the connection and writing to stdinPipe (relay data from client to WSL)
	log.Println("Forwarding request from connection to stdin pipe")
	for {
		log.Println("Reading from connection")
		// Read from connection and write to unix socket connection
		buf := make([]byte, bufferSize)
		n, err := conn.Read(buf)
		if err != nil {
			if err == io.EOF {
				log.Println("EOF from connection")
				break
			}
			if err, ok := err.(net.Error); ok && err.Timeout() {
				log.Println("[NAMED-PIPE] Deadline reached - read response")
				break
			}
			log.Printf("Error reading from connection: %v\n", err)
			break
		}
		if n == 0 {
			log.Println("EOF from connection - zero bytes read")
			break
		}
		n, err = stdinPipe.Write(buf[:n])
		if err != nil {
			log.Printf("Error copying from connection to unix socket connection: %v\n", err)
			break
		} else {
			log.Printf("Copied %d bytes from connection to unix socket connection\n", n)
		}
	}

	// log.Println("Request relay complete - closing stdin pipe")
	// stdinPipe.Close()

	// Response - Read from stdout pipe and write to connection
	// Reading from stdoutPipe and writing to connection (relay data from WSL back to client)
	log.Println("Forwarding response from stdout to named pipe connection")

	for {
		log.Println("Reading from stdout")
		// Read from connection and write to named pipe connection
		buf := make([]byte, bufferSize)
		// n, err := stdoutPipe.Read(buf)
		n, err := readWithDeadline(stdoutPipe, buf, 1000*time.Millisecond)
		if err != nil {
			if err == io.EOF {
				log.Println("EOF from stdoutPipe")
			} else if err.Error() == "Timeout" {
				log.Println("Timeout while reading from stdout pipe after")
			} else {
				log.Printf("Error reading from stdout pipe: %v\n", err)
			}
			log.Printf("Error reading from stdoutPipe: %v\n", err)
			break
		}
		if n == 0 {
			log.Println("EOF from stdoutPipe - zero bytes read")
			break
		}
		log.Printf("Read %d bytes from stdoutPipe and forward them to named pipe connection: %s\n", n, string(buf[:n]))
		n, err = conn.Write(buf[:n])
		if err != nil {
			log.Printf("Error copying from stdoutPipe to named pipe connection: %v\n", err)
			break
		} else {
			log.Printf("Copied %d bytes from stdoutPipe to named pipe connection\n", n)
		}
	}

	log.Println("WINDOWS NAMED PIPE - Relaying complete")
	conn.Write(nil)
	conn.Close()
	cmd.Cancel()
}

func main() {
	flag.Parse()

	// Handle program exit
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

	// Termination signals
	go func() {
		<-signalChan
		log.Println("Received termination signal")
		signal.Stop(signalChan)
		log.Println("Canceling context")
		cancelFunc()
		log.Println("Exiting")
		os.Exit(0)
	}()

	// Parse security descriptor
	securityDescriptor := AllowEveryone
	if len(permissions) > 0 {
		switch permissions {
		case "AllowServiceSystemAdmin":
			securityDescriptor = AllowServiceSystemAdmin
		case "AllowCurrentUser":
			securityDescriptor = AllowCurrentUser
		case "AllowEveryone":
			securityDescriptor = AllowEveryone
		default:
			securityDescriptor = permissions
		}
		if strings.Contains(securityDescriptor, "$SID") {
			currentUser, err := user.Current()
			if err != nil {
				log.Println("Relay server error retrieving current user:", err)
				return
			}
			securityDescriptor = strings.Replace(securityDescriptor, "$SID", currentUser.Uid, 1)
		}
		log.Printf("Computed permissions are: %s\n", securityDescriptor)
	}

	// Write the PID of current process
	if len(pidFile) > 0 {
		log.Printf("Writing relay Windows PID %d to %s\n", os.Getpid(), pidFile)
		pidContents := []byte(strconv.FormatInt(int64(os.Getpid()), 10))
		err := os.WriteFile(pidFile, pidContents, 0644)
		if err != nil {
			panic(err)
		}
	}

	if parentPid > 0 {
		log.Printf("Reported parent process ID is %d\n", parentPid)
		// if os.Getppid() != int(parentPid) {
		// 	log.Fatalf("Parent process ID %d does not match expected %d", os.Getppid(), parentPid)
		// }
	}

	// Configure named pipe
	pc := &winio.PipeConfig{
		SecurityDescriptor: securityDescriptor,
		MessageMode:        false,
		InputBufferSize:    int32(bufferSize),
		OutputBufferSize:   int32(bufferSize),
	}
	// Listen on the named pipe
	listener, err := winio.ListenPipe(namedPipe, pc)
	if err != nil {
		log.Fatal("Relay server listen error:", err)
	}
	defer listener.Close()
	log.Printf("Relay server is now listening on: %s\n", namedPipe)
	go watchParentProcess(os.Getppid())
	cmdOpts := []string{
		unixSocket,
	}
	if len(relayProgramOptions) > 0 {
		cmdOpts = append(cmdOpts, relayProgramOptions)
	}
	cmdArgs := []string{
		"--distribution", distribution,
		"--exec",
		relayProgramPath,
		"STDIO",
		fmt.Sprintf("UNIX-CONNECT:%s", strings.Join(cmdOpts, ",")),
		// "--unix-socket",
		// strings.Join(cmdOpts, ","),
	}
	log.Println("Waiting for client connections")
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Fatal("Relay server accept error:", err)
		}
		go handleClient(conn, cmdArgs, ctx)
	}
}
