//go:build windows
// +build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/containers/gvisor-tap-vsock/pkg/sshclient"
	"github.com/containers/winquit/pkg/winquit"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"
)

var (
	namedPipe        string
	sshConnection    string
	sshTimeout       int
	identityPath     string
	tidPath          string
	parentProcessPid int
	// Relay arguments
	distribution            string
	relayProgramPath        string
	watchProcessTermination bool
	generateKeyPair         bool
	port                    int
	host                    string
	bufferSize              int
	pollInterval            int
)

var relayProgramPid int = -1

func init() {
	flag.StringVar(&namedPipe, "named-pipe", "npipe:////./pipe/container-desktop", "Named pipe to relay through")
	flag.StringVar(&sshConnection, "ssh-connection", "ssh://ubuntu@localhost:50022/var/run/docker.sock", "The SSH connection string")
	flag.IntVar(&sshTimeout, "ssh-timeout", 5, "The SSH connection timeout in seconds")
	flag.StringVar(&identityPath, "identity-path", "", "Path to the SSH connection private key")
	flag.StringVar(&tidPath, "tid-path", "", "Thread ID file path")
	// Relay arguments
	flag.StringVar(&distribution, "distribution", "Ubuntu", "The WSL distribution")
	flag.StringVar(&relayProgramPath, "relay-program-path", "", "Path to the relay program")
	flag.StringVar(&host, "host", "127.0.0.1", "The SSH connection host")
	flag.IntVar(&port, "port", 20022, "The SSH connection port")
	flag.IntVar(&bufferSize, "buffer-size", 4096, "The I/O buffer size")
	flag.BoolVar(&generateKeyPair, "generate-key-pair", false, "Generate SSH RSA key pair - it overwrites existing key pair")
	flag.BoolVar(&watchProcessTermination, "watch-process-termination", false, "Watch for process termination(WSL patch)")
	flag.IntVar(&pollInterval, "poll-interval", 2, "Parent process polling interval in seconds - default is 2 seconds")
	// Flags
	flag.Usage = func() {
		flag.PrintDefaults()
	}
	log.SetPrefix("[windows]")
	log.SetOutput(os.Stderr)
}

func testSSHConnection() {
	dest, err := url.Parse(sshConnection)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Testing SSH connection to %s\n", dest)
	// ssh config
	if err != nil {
		log.Fatal(err)
	}
	user := dest.User.Username()
	// Methods
	auth := []ssh.AuthMethod{}
	if len(identityPath) > 0 {
		key, err := os.ReadFile(identityPath)
		if err != nil {
			log.Fatalf("Unable to read private key: %v\n", err)
		}
		log.Printf("Parsing private key from %s\n", identityPath)
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			log.Fatalf("Unable to parse private key: %v\n", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         time.Second * time.Duration(sshTimeout),
	}
	// connect to ssh server
	conn, err := ssh.Dial("tcp", dest.Host, config)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
}

func testNamedPipe() {
	dest, err := url.Parse(namedPipe)
	if err != nil {
		log.Fatal(err)
	}
	// Ensure named pipe is not already opened
	pipePath := strings.ReplaceAll(dest.Path, "/", "\\")
	log.Printf("Trying to open named pipe %s\n", pipePath)
	f, err := winio.DialPipe(pipePath, nil)
	if err != nil {
		log.Println("Pipe is not opened, good...")
	} else {
		f.Close()
		log.Fatalf("Pipe already opened\n")
	}
}

func getWSLPath(distribution string, windowsPath string) (string, error) {
	cmd := exec.Command("wsl.exe", "--distribution", distribution, "--exec", "wslpath", windowsPath)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		log.Printf("Error getting WSL path: %v\n", err)
		return "", err
	}
	log.Printf("WSL path for %s: %s\n", windowsPath, string(out))
	return strings.TrimSpace(string(out)), nil
}

func startRelayProgram(ctx context.Context) error {
	// Start the relay program
	wslIdentityPath, err := getWSLPath(distribution, identityPath)
	if err != nil {
		log.Printf("Error getting WSL path: %v\n", err)
		return err
	}
	args := []string{
		"--distribution",
		distribution,
		"--exec",
		relayProgramPath,
		"--host", host,
		"--port", fmt.Sprintf("%d", port),
		"--buffer-size", fmt.Sprintf("%d", bufferSize),
		"--poll-interval", fmt.Sprintf("%d", pollInterval),
		"--identity-path", wslIdentityPath,
		"--parent-process-pid", strconv.Itoa(os.Getpid()),
	}
	if watchProcessTermination {
		args = append(args, "--watch-process-termination")
	}
	if generateKeyPair {
		args = append(args, "--generate-key-pair")
	}
	log.Printf("Starting relay program with args: wsl.exe %s\n", strings.Join(args, " "))
	relay := exec.CommandContext(ctx, "wsl.exe", args...)
	relay.SysProcAttr = &syscall.SysProcAttr{
		// CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
		// HideWindow: false,
	}
	relay.Stdout = os.Stdout
	relay.Stderr = os.Stderr
	if err := relay.Start(); err != nil {
		log.Fatalf("Error starting relay program: %v\n", err)
		return err
	}
	relayProgramPid = relay.Process.Pid
	log.Printf("Relay program started with PID: %d\n", relayProgramPid)
	return relay.Wait()
}

func main() {
	flag.Parse()
	log.Println("Starting container-desktop-ssh-relay")

	testNamedPipe()

	if len(tidPath) > 0 {
		_, err := saveThreadId(tidPath)
		if err != nil {
			log.Fatalf("Error saving thread ID: %v\n", err)
		}
	}

	ctx, cancelFunc := context.WithCancel(context.Background())
	group, ctx := errgroup.WithContext(ctx)
	defer cancelFunc()

	if len(relayProgramPath) > 0 {
		log.Println("Starting relay program")
		group.Go(func() error {
			return startRelayProgram(ctx)
		})
	}

	time.Sleep(5 * time.Second)
	testSSHConnection()

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan,
		os.Interrupt,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT,
		syscall.SIGSEGV)
	go func() {
		<-stopChan
		log.Println("Received termination signal")
		signal.Stop(stopChan)
		cancelFunc()
		if relayProgramPid > -1 {
			log.Printf("Killing relay program with PID: %d\n", relayProgramPid)
			err := exec.Command("taskkill", "/f", "/pid", strconv.Itoa(relayProgramPid)).Run()
			if err != nil {
				log.Printf("Error killing relay program %d: %v\n", relayProgramPid, err)
			}
		}
		os.Exit(0)
	}()

	log.Println("Setting up proxies started")
	err := setupProxies(ctx, group, namedPipe, sshConnection, identityPath)
	if err != nil {
		log.Fatalf("Unable to setup proxies: %s\n", err.Error())
	}

	log.Println("Setting up proxies completed - waiting for worker group")
	if err := group.Wait(); err != nil {
		log.Fatalf("Error occurred in execution group: %s\n", err.Error())
	}
}

func setupProxies(ctx context.Context, g *errgroup.Group, source string, destination string, identity string) error {
	var (
		src  *url.URL
		dest *url.URL
		err  error
	)
	if strings.Contains(source, "://") {
		src, err = url.Parse(source)
		if err != nil {
			return err
		}
	} else {
		src = &url.URL{
			Scheme: "unix",
			Path:   source,
		}
	}

	dest, err = url.Parse(destination)
	if err != nil {
		return err
	}

	g.Go(func() error {
		log.Printf("Creating SSH relay from %s to %s\n", src.String(), dest.String())
		forward, err := sshclient.CreateSSHForward(ctx, src, dest, identity, nil)
		if err != nil {
			return err
		}
		log.Printf("Forwarding %s to %s\n", src.String(), dest.String())
		go func() {
			<-ctx.Done()
			// Abort pending accepts
			log.Println("Closing forward")
			forward.Close()
		}()
	loop:
		for {
			select {
			case <-ctx.Done():
				break loop
			default:
				// proceed
			}
			err := forward.AcceptAndTunnel(ctx)
			if err != nil {
				log.Fatalf("Error occurred handling ssh forwarded connection: %q\n", err)
			}
		}
		return nil
	})

	return nil
}

func saveThreadId(path string) (uint32, error) {
	stateDir := filepath.Dir(path)
	if _, err := os.Stat(stateDir); os.IsNotExist(err) {
		if err := os.MkdirAll(stateDir, 0755); err != nil {
			log.Println("Error creating state directory: " + err.Error())
			os.Exit(1)
		}
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0644)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	tid := winquit.GetCurrentMessageLoopThreadId()
	fmt.Fprintf(file, "%d:%d\n", os.Getpid(), tid)
	return tid, nil
}
