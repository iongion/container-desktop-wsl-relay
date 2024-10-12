package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/csv"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"
)

var (
	watchProcessTermination bool
	generateKeyPair         bool
	port                    int
	host                    string
	identityPath            string
	bufferSize              int
	pollInterval            int
	parentProcessPid        int
)

var cancelFunc context.CancelFunc

const (
	PRCTL_SYSCALL    = 157
	PR_SET_PDEATHSIG = 1
)

type streamLocalDirect struct {
	SocketPath string
	Reserved0  string
	Reserved1  uint32
}

func init() {
	flag.StringVar(&host, "host", "127.0.0.1", "The SSH connection host")
	flag.IntVar(&port, "port", 20022, "The SSH connection port")
	flag.IntVar(&bufferSize, "buffer-size", 4096, "The I/O buffer size")
	flag.StringVar(&identityPath, "identity-path", "", "The SSH connection identity")
	flag.BoolVar(&generateKeyPair, "generate-key-pair", false, "Generate SSH RSA key pair - it overwrites existing key pair")
	flag.BoolVar(&watchProcessTermination, "watch-process-termination", false, "Watch for process termination(WSL patch)")
	flag.IntVar(&pollInterval, "poll-interval", 2, "Parent process polling interval in seconds - default is 2 seconds")
	flag.IntVar(&parentProcessPid, "parent-process-pid", -1, "Parent process PID(Windows PID) - used to watch for process termination")
	flag.Usage = func() {
		flag.PrintDefaults()
	}
	log.SetPrefix("[linux]")
	log.SetOutput(os.Stderr)
}

func handleRequests(reqs <-chan *ssh.Request) {
	for range reqs {
		log.Println("Received request")
	}
}

func cleanupSockAndChannel(sock net.Conn, channel ssh.Channel) {
	err := sock.Close()
	if err != nil {
		log.Printf("Error closing sock: %v\n", err)
	}
	channel.CloseWrite()
	if err != nil {
		log.Printf("Error closing channel write: %v\n", err)
	}
	err = channel.Close()
	if err != nil {
		log.Printf("Error closing channel: %v\n", err)
	}
}

func handleChannels(chans <-chan ssh.NewChannel) {
	directMsg := streamLocalDirect{}
	for newChannel := range chans {
		if t := newChannel.ChannelType(); t != "direct-streamlocal@openssh.com" {
			newChannel.Reject(ssh.UnknownChannelType, fmt.Sprintf("Channel type is no supported: %s", t))
			continue
		}
		if err := ssh.Unmarshal(newChannel.ExtraData(), &directMsg); err != nil {
			log.Printf("Could not direct-streamlocal data: %s\n", err)
			newChannel.Reject(ssh.Prohibited, "invalid format")
			return
		}
		channel, _, err := newChannel.Accept()
		if err != nil {
			log.Printf("Could not accept channel: %s\n", err)
			continue
		}

		// Handle channel
		socketPath := directMsg.SocketPath
		if len(socketPath) == 0 {
			log.Println("Channel socket path must be provided")
			newChannel.Reject(ssh.Prohibited, "Channel socket path must be provided")
			return
		}
		log.Printf("Connecting to socket: <%s>\n", socketPath)
		sock, err := net.Dial("unix", socketPath)
		if err != nil {
			log.Printf("Could not dial unix socket %s: %v\n", socketPath, err)
		}

		defer func() {
			log.Println("Completion - closing sock and channel")
			cleanupSockAndChannel(sock, channel)
		}()

		sock.SetReadDeadline(time.Now().Add(5 * time.Second))

		// I/O operations are done in separate goroutines
		var wg sync.WaitGroup
		defer wg.Wait()

		// Reading from the channel and writing to the socket
		wg.Add(1)
		go func() {
			buffer := make([]byte, bufferSize)
			for {
				n, err := channel.Read(buffer)
				if err != nil {
					if err != io.EOF {
						// log.Printf("Error reading from channel: %v\n", err)
					} else {
						// log.Println("Channel read complete - EOF reached")
					}
					break
				}
				if n > 0 {
					_, writeErr := sock.Write(buffer[:n])
					if writeErr != nil {
						// log.Printf("Error writing to sock: %v\n", writeErr)
						break
					}
					// log.Printf("Wrote %d bytes from channel to sock\n", n)
				}
			}
			wg.Done()
		}()

		// Reading from the socket and writing to the channel
		wg.Add(1)
		go func() {
			buffer := make([]byte, bufferSize)
			for {
				n, err := sock.Read(buffer)
				if err != nil {
					if err != io.EOF {
						// log.Printf("Error reading from sock: %v\n", err)
					} else {
						// log.Println("Sock read complete - EOF reached")
					}
					break
				}
				if n > 0 {
					_, writeErr := channel.Write(buffer[:n])
					if writeErr != nil {
						// log.Printf("Error writing to channel: %v\n", writeErr)
						break
					}
					// log.Printf("Wrote %d bytes from sock to channel\n", n)
				}
			}
			// log.Println("Read complete or timeout - closing sock and channel")
			cleanupSockAndChannel(sock, channel)
			wg.Done()
		}()
	}
}

func handleConnection(conn net.Conn, sshConfig *ssh.ServerConfig) {
	ssh_conn, channels, requests, err := ssh.NewServerConn(conn, sshConfig)
	if err != nil {
		log.Fatalf("Unable to relay TCP to SSH: %v\n", err)
	}
	log.Printf("Logged in with key %s\n", ssh_conn.Permissions.Extensions["pubkey-fp"])
	var wg sync.WaitGroup
	defer wg.Wait()

	wg.Add(1)
	go func() {
		ssh.DiscardRequests(requests)
		wg.Done()
	}()

	wg.Add(1)
	go func(in <-chan *ssh.Request) {
		handleRequests(in)
		wg.Done()
	}(requests)

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
		}()
		handleChannels(channels)
	}()
}

func writeKeyPair(filename string) {
	baseDir := filepath.Dir(filename)
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		log.Fatalln("Error creating base directory: " + err.Error())
		stopChan <- syscall.SIGINT
		return
	}

	bitSize := 4096
	// Generate RSA key.
	key, err := rsa.GenerateKey(rand.Reader, bitSize)
	if err != nil {
		panic(err)
	}
	// Extract public component.
	pub := key.Public()
	// Encode private key to PKCS#1 ASN.1 PEM.
	privateKeyPEM := pem.EncodeToMemory(
		&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(key),
		},
	)
	// Encode public key to PKCS#1 ASN.1 PEM.
	publicKeyPEM := pem.EncodeToMemory(
		&pem.Block{
			Type:  "RSA PUBLIC KEY",
			Bytes: x509.MarshalPKCS1PublicKey(pub.(*rsa.PublicKey)),
		},
	)
	// Write private key to file.
	if err := os.WriteFile(filename, privateKeyPEM, 0700); err != nil {
		panic(err)
	}
	// Write public key to file.
	if err := os.WriteFile(filename+".pub", publicKeyPEM, 0755); err != nil {
		panic(err)
	}
}

func nativeProcessExits(pid int32) (bool, error) {
	if pid <= 0 {
		return false, fmt.Errorf("invalid pid %v", pid)
	}
	proc, err := os.FindProcess(int(pid))
	if err != nil {
		return false, err
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true, nil
	}
	if err.Error() == "os: process already finished" {
		return false, nil
	}
	errno, ok := err.(syscall.Errno)
	if !ok {
		return false, err
	}
	switch errno {
	case syscall.ESRCH:
		return false, nil
	case syscall.EPERM:
		return true, nil
	}
	return false, err
}

func isProcessRunning(ctx context.Context, windowsPid int, linuxPid int) bool {
	// Detecting if a process is running on Linux
	flag, err := nativeProcessExits(int32(linuxPid))
	if err != nil {
		log.Printf("Error checking process: %v\n", err)
	}
	if !flag {
		log.Printf("Linux process %d is no longer running - shutting down\n", linuxPid)
		return false
	}
	// Detecting if a process is running on Windows
	cmd := exec.CommandContext(ctx, "tasklist.exe", "/fo", "CSV", "/fi", fmt.Sprintf("PID eq %d", windowsPid))
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		log.Printf("Error checking process: %v\n", err)
		return false
	}
	cleaned := strings.TrimSpace(string(out))
	csvReader := csv.NewReader(strings.NewReader(cleaned))
	records, err := csvReader.ReadAll()
	if err != nil {
		log.Printf("Error parsing process list CSV: %v\n", err)
		return false
	}
	// log.Printf("Process running check: %s - %v\n", cleaned, records)
	return len(records) > 1 && cmd.ProcessState.Success()
}

var stopChan = make(chan os.Signal, 1)

func watchProcess(ctx context.Context, processPid int) {
	for {
		if processPid > 0 {
			if isProcessRunning(ctx, processPid, os.Getpid()) {
				// log.Printf("Process %d is still running\n", processPid)
				// buffer := []byte(fmt.Sprintf("%s Process %d is still running\n", strconv.Itoa(int(time.Now().Unix())), processPid))
				// os.WriteFile("/tmp/process-log.txt", buffer, 0644)
			} else {
				log.Printf("Process %d is no longer running - shutting down\n", processPid)
				// buffer := []byte(fmt.Sprintf("%s Process %d is no longer running\n", strconv.Itoa(int(time.Now().Unix())), processPid))
				// os.WriteFile("/tmp/process-log.txt", buffer, 0644)
				break
			}
		}
		time.Sleep(time.Duration(pollInterval) * time.Second)
	}
	stopChan <- syscall.SIGINT
	os.Exit(0)
}

func setKillSignal() {
	_, _, errno := syscall.RawSyscall(uintptr(PRCTL_SYSCALL), uintptr(PR_SET_PDEATHSIG), uintptr(syscall.SIGKILL), 0)
	if errno != 0 {
		log.Printf("Error setting parent death signal: %v\n", errno)
		os.Exit(127 + int(errno))
	}
	// here's the check that prevents an orphan due to the possible race
	// condition
	// if strconv.Itoa(os.Getppid()) != os.Getenv("PARENT_PID") {
	// 	os.Exit(1)
	// }
}

func main() {
	flag.Parse()

	setKillSignal()

	address := fmt.Sprintf("%s:%d", host, port)
	log.Printf("Starting ssh server on %s\n", address)

	ctx, cancelFunc := context.WithCancel(context.Background())
	group, ctx := errgroup.WithContext(ctx)
	defer cancelFunc()

	signal.Notify(stopChan,
		os.Kill,
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
		log.Println("Exiting")
		os.Exit(0)
	}()

	if watchProcessTermination {
		log.Printf("Watching process termination - spawned by parent pid %d as pid %d\n", parentProcessPid, os.Getpid())
		// Note - This is a WSL specific hack otherwise the process does not terminate
		// See - https://github.com/golang/go/issues/69845
		// go watchProcess(ctx, parentProcessPid)
	} else {
		log.Println("Not watching process termination")
	}

	if generateKeyPair {
		if len(identityPath) == 0 {
			log.Fatal("Identity path must be specified when generating key pair - exiting")
		}
		log.Printf("Identity path %s - keypair generation started\n", identityPath)
		writeKeyPair(identityPath)
	}

	// Read the private key from the identity path
	log.Println("Reading private key from", identityPath)
	hostKeyBuffer, err := os.ReadFile(identityPath)
	if err != nil {
		log.Fatalf("Unable to read private key: %v\n", err)
	}
	privateKey, err := ssh.ParsePrivateKey(hostKeyBuffer)
	if err != nil {
		log.Fatalf("Unable to parse private key: %v\n", err)
	}

	// Read the public key from the identity path
	log.Printf("Reading public key from %s.pub\n", identityPath)
	pubKeyBuffer, err := os.ReadFile(fmt.Sprintf("%s.pub", identityPath))
	if err != nil {
		log.Fatalf("Unable to read public key: %v\n", err)
	}
	pemBlock, rest := pem.Decode(pubKeyBuffer)
	if pemBlock == nil {
		log.Fatalf("invalid PEM public key passed, pem.Decode() did not find a public key\n")
	}
	if len(rest) > 0 {
		log.Fatalf("PEM block contains more than just public key\n")
	}
	rsaPubKey, err := x509.ParsePKCS1PublicKey(pemBlock.Bytes)
	if err != nil {
		log.Fatalf("Unable to parse public key: %v\n", err)
	}
	publicKey, err := ssh.NewPublicKey(rsaPubKey)
	publicKeyPEM := ssh.MarshalAuthorizedKey(publicKey)

	sshConfig := &ssh.ServerConfig{
		NoClientAuth: false,
		PublicKeyCallback: func(conn ssh.ConnMetadata, connectionPublicKey ssh.PublicKey) (*ssh.Permissions, error) {
			log.Printf("Login attempt by %s\n", conn.User())
			connectionPublicKeyPEM := ssh.MarshalAuthorizedKey(publicKey)
			matching := bytes.Compare(publicKeyPEM, connectionPublicKeyPEM) == 0
			if matching {
				return &ssh.Permissions{
					Extensions: map[string]string{
						"pubkey-fp": ssh.FingerprintSHA256(connectionPublicKey),
					},
				}, nil
			}
			return nil, fmt.Errorf("Keys are not matching - Unknown public key for %q", conn.User())
		},
	}

	sshConfig.AddHostKey(privateKey)

	log.Printf("Starting listening for SSH connections on %s\n", address)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		log.Printf("Unable to listen to address: %v\n", err)
		stopChan <- syscall.SIGINT
		return
	}

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		default:
			// proceed
		}
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Unable to accept listener: %v\n", err)
			break
		}
		defer func() {
			log.Println("Closing connection")
			err = conn.Close()
			if err != nil {
				log.Printf("Unable to close connection: %v\n", err)
			}
		}()
		go handleConnection(conn, sshConfig)
	}

	log.Println("Waiting for worker group")
	if err := group.Wait(); err != nil {
		log.Printf("Error occurred in execution group: %s\n", err.Error())
	}

	stopChan <- syscall.SIGINT
}
