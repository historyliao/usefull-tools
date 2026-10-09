package sshcmd

import (
	"strings"
	"testing"

	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/config"
)

func TestArgsReverseSocks(t *testing.T) {
	def := config.Normalize(config.Definition{
		Name:      "remote-socks",
		Direction: config.DirectionRev,
		Target:    config.Target{User: "lyy", Host: "example.com"},
		Forwards:  []config.Forward{{Type: "R", BindHost: "127.0.0.1", BindPort: 1080}},
		Restart:   config.RestartOnFail,
		Backoff:   config.DefaultBackoff(),
	})
	args := strings.Join(Args("ssh", def), " ")
	if !strings.Contains(args, "-R 127.0.0.1:1080 ") {
		t.Fatalf("缺少远端 SOCKS 的 -R 参数: %s", args)
	}
	if strings.Contains(args, "127.0.0.1:1080:") {
		t.Fatalf("远端 SOCKS 不应带目标: %s", args)
	}
}
