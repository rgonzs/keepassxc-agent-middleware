package transport

import (
	"context"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func Dial(ctx context.Context, endpoint string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, endpoint)
}

func currentSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

func Listen(endpoint string) (net.Listener, error) {
	sid, err := currentSID()
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(endpoint, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")",
		InputBufferSize:    65536, OutputBufferSize: 65536,
	})
}

func DefaultListen() string {
	sid, err := currentSID()
	if err != nil {
		return ""
	}
	return `\\.\pipe\keepassxc-agent-` + sid
}

func DefaultUpstream() string { return `\\.\pipe\openssh-ssh-agent` }

func SameEndpoint(a, b string) bool {
	return strings.EqualFold(strings.ReplaceAll(a, "/", `\`), strings.ReplaceAll(b, "/", `\`))
}
