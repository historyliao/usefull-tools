package config_test

import (
	"testing"

	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/config"
)

func TestParseForwardReverseSocks(t *testing.T) {
	f, err := config.ParseForward("R", "127.0.0.1:1080")
	if err != nil {
		t.Fatalf("无目标的反向转发应可解析: %v", err)
	}
	if !f.IsRemoteSocks() {
		t.Fatalf("应识别为远端 SOCKS: %+v", f)
	}
	if got := f.Spec(); got != "127.0.0.1:1080" {
		t.Fatalf("Spec = %q，期望 127.0.0.1:1080", got)
	}
}

func TestParseForwardReverseWithDest(t *testing.T) {
	f, err := config.ParseForward("R", "127.0.0.1:9000:192.168.179.3:9000")
	if err != nil {
		t.Fatal(err)
	}
	if f.IsRemoteSocks() {
		t.Fatal("带目标的反向转发不应识别为远端 SOCKS")
	}
	if got := f.Spec(); got != "127.0.0.1:9000:192.168.179.3:9000" {
		t.Fatalf("Spec = %q", got)
	}
}

func TestParseForwardLocalRequiresDest(t *testing.T) {
	if _, err := config.ParseForward("L", "127.0.0.1:1080"); err == nil {
		t.Fatal("-L 缺少目标应报错")
	}
}

func TestValidateReverseSocks(t *testing.T) {
	def := config.Normalize(config.Definition{
		Name:      "remote-socks",
		Direction: config.DirectionRev,
		Target:    config.Target{User: "lyy", Host: "example.com", Port: 22},
		Forwards:  []config.Forward{{Type: "R", BindHost: "127.0.0.1", BindPort: 1080}},
		Restart:   config.RestartOnFail,
		Backoff:   config.DefaultBackoff(),
	})
	if err := config.Validate(def); err != nil {
		t.Fatalf("远端 SOCKS 定义应通过校验: %v", err)
	}
}

func TestValidateReverseRejectsPartialDest(t *testing.T) {
	def := config.Normalize(config.Definition{
		Name:      "bad",
		Direction: config.DirectionRev,
		Target:    config.Target{User: "lyy", Host: "example.com", Port: 22},
		Forwards:  []config.Forward{{Type: "R", BindHost: "127.0.0.1", BindPort: 1080, DestPort: 80}},
		Restart:   config.RestartOnFail,
		Backoff:   config.DefaultBackoff(),
	})
	if err := config.Validate(def); err == nil {
		t.Fatal("只填目标端口、缺目标主机应报错")
	}
}
