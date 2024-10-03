//go:build linux

package main

import (
	"context"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var (
	unixSocket string
)

var signalChan chan (os.Signal) = make(chan os.Signal, 1)

func init() {
	flag.StringVar(&unixSocket, "unix-socket", "/var/run/docker.sock", "The Unix socket to relay through")
	flag.Usage = func() {
		flag.PrintDefaults()
	}
	log.SetPrefix("[linux]")
	log.SetOutput(os.Stderr)
	// logFile, err := os.OpenFile("container-desktop-wsl-relay.exe.log", os.O_CREATE|os.O_APPEND|os.O_RDWR, 0666)
	// if err != nil {
	// 	panic(err)
	// }
	// mw := io.MultiWriter(os.Stderr, logFile)
	// log.SetOutput(mw)
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
	_, cancelFunc := context.WithCancel(context.Background())
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

	log.Printf("Connecting to Unix socket [%s]\n", unixSocket)

	conn, err := net.Dial("unix", unixSocket)
	if err != nil {
		log.Fatalf("Error connecting to Unix socket [%s]: %v\n", unixSocket, err)
	}
	defer conn.Close()

	err = conn.SetReadDeadline(time.Now().Add(1000 * time.Millisecond))
	if err != nil {
		log.Println("Error setting read deadline:", err)
		return
	}

	log.Printf("Connected to Unix socket [%s]\n", unixSocket)

	log.Println("Relaying from stdin to unix socket connection")
	for {
		log.Println("Reading from stdin")
		// Read from stdin and write to unix socket connection
		buf := make([]byte, 1024)
		n, err := os.Stdin.Read(buf)
		if err != nil {
			if err == io.EOF {
				log.Println("EOF from stdin")
				break
			}
			log.Printf("Error reading from stdin: %v\n", err)
			break
		}
		if n == 0 {
			log.Println("EOF from stdin")
			break
		}
		n, err = conn.Write(buf[:n])
		if err != nil {
			log.Printf("Error copying from stdin to unix socket connection: %v\n", err)
			break
		} else {
			log.Printf("Copied %d bytes from stdin to unix socket connection\n", n)
		}
	}

	log.Println("Relaying from unix socket connection to stdout")
	for {
		log.Println("Reading from connection")
		// Read from unix socket connection and write to stdout
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			if err == io.EOF {
				log.Println("EOF from unix socket connection")
				break
			}
			if err, ok := err.(net.Error); ok && err.Timeout() {
				log.Println("[UNIX-SOCKET] Deadline reached - read response")
				break
			}
			log.Printf("Error reading from unix socket connection: %v\n", err)
		}
		if n == 0 {
			log.Println("EOF from unix socket connection")
			break
		}
		n, err = os.Stdout.Write(buf[:n])
		if err != nil {
			log.Printf("Error copying from unix socket connection to stdout: %v\n", err)
			break
		} else {
			log.Printf("Copied %d bytes from unix socket connection to stdout\n", n)
		}
	}

	log.Println("UNIX SOCKET - Relaying complete")
}
