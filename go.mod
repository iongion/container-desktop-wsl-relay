module container-desktop-ssh-relay

go 1.23.0

replace (
	github.com/gogo/protobuf => github.com/gogo/protobuf v1.3.2
	golang.org/x/crypto => golang.org/x/crypto v0.0.0-20201216223049-8b5274cf687f
	golang.org/x/text => golang.org/x/text v0.3.3
)

require (
	github.com/containers/gvisor-tap-vsock v0.7.5
	github.com/containers/winquit v1.1.0
	github.com/keybase/go-ps v0.0.0-20190827175125-91aafc93ba19
	golang.org/x/sync v0.8.0
)

require (
	github.com/Microsoft/go-winio v0.6.2
	github.com/sirupsen/logrus v1.9.3 // indirect
)

require (
	github.com/pkg/errors v0.9.1 // indirect
	golang.org/x/crypto v0.28.0
	golang.org/x/sys v0.26.0 // indirect
)
