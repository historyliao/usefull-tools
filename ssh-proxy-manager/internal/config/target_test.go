package config_test

import (
	"path/filepath"
	"testing"

	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/config"
)

func sampleDefinition(name, ref string) config.Definition {
	return config.Normalize(config.Definition{
		Name:      name,
		Direction: config.DirectionRev,
		TargetRef: ref,
		Forwards: []config.Forward{
			{Type: "R", BindHost: "127.0.0.1", BindPort: 9000, DestHost: "10.0.0.1", DestPort: 80},
		},
		Restart: config.RestartOnFail,
		Backoff: config.DefaultBackoff(),
	})
}

func TestResolveTargetUsesReference(t *testing.T) {
	cfg := &config.Config{Version: config.Version}
	if err := cfg.UpsertTarget(config.SSHTarget{Name: "hhdev", User: "lyy", Host: "example.com", Port: 12880, Identity: "~/.ssh/id_rsa"}); err != nil {
		t.Fatal(err)
	}
	def := sampleDefinition("p1", "hhdev")
	if err := cfg.ValidateDefinition(def); err != nil {
		t.Fatalf("引用存在的目标应通过校验: %v", err)
	}
	target, err := cfg.ResolveTarget(def)
	if err != nil {
		t.Fatal(err)
	}
	if target.User != "lyy" || target.Host != "example.com" || target.Port != 12880 || target.Identity != "~/.ssh/id_rsa" {
		t.Fatalf("解析出的目标不对: %+v", target)
	}
}

func TestResolveTargetFallsBackToInline(t *testing.T) {
	cfg := &config.Config{Version: config.Version}
	def := sampleDefinition("p2", "")
	def.Target = config.Target{User: "root", Host: "10.0.0.9", Port: 2222}
	target, err := cfg.ResolveTarget(def)
	if err != nil {
		t.Fatal(err)
	}
	if target.Host != "10.0.0.9" || target.Port != 2222 {
		t.Fatalf("内联目标解析错误: %+v", target)
	}
}

func TestValidateDefinitionRejectsUnknownReference(t *testing.T) {
	cfg := &config.Config{Version: config.Version}
	def := sampleDefinition("p3", "not-exist")
	if err := cfg.ValidateDefinition(def); err == nil {
		t.Fatal("引用不存在的目标应当报错")
	}
}

func TestValidateDefinitionWithReferenceSkipsInlineChecks(t *testing.T) {
	cfg := &config.Config{Version: config.Version}
	if err := cfg.UpsertTarget(config.SSHTarget{Name: "hhdev", User: "lyy", Host: "example.com", Port: 12880}); err != nil {
		t.Fatal(err)
	}
	def := sampleDefinition("p4", "hhdev")
	def.Target = config.Target{}
	if err := cfg.ValidateDefinition(def); err != nil {
		t.Fatalf("有引用时不应再要求内联主机: %v", err)
	}
}

func TestRemoveTargetRejectedWhenReferenced(t *testing.T) {
	cfg := &config.Config{Version: config.Version}
	if err := cfg.UpsertTarget(config.SSHTarget{Name: "hhdev", User: "lyy", Host: "example.com", Port: 12880}); err != nil {
		t.Fatal(err)
	}
	cfg.Proxies = append(cfg.Proxies, sampleDefinition("p5", "hhdev"))
	err := cfg.RemoveTarget("hhdev")
	if err == nil {
		t.Fatal("目标被引用时应拒绝删除")
	}
	if usage := cfg.TargetUsage("hhdev"); len(usage) != 1 || usage[0] != "p5" {
		t.Fatalf("引用列表不对: %v", usage)
	}
	cfg.Proxies = nil
	if err := cfg.RemoveTarget("hhdev"); err != nil {
		t.Fatalf("无引用时应可删除: %v", err)
	}
}

func TestConfigRoundTripKeepsTargets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := &config.Config{Version: config.Version}
	if err := cfg.UpsertTarget(config.SSHTarget{Name: "hhdev", User: "lyy", Host: "example.com", Port: 12880, ExtraArgs: []string{"-o", "Foo=bar"}}); err != nil {
		t.Fatal(err)
	}
	cfg.Proxies = append(cfg.Proxies, sampleDefinition("p6", "hhdev"))
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Targets) != 1 || loaded.Targets[0].Name != "hhdev" || loaded.Targets[0].Port != 12880 {
		t.Fatalf("目标未正确持久化: %+v", loaded.Targets)
	}
	if len(loaded.Proxies) != 1 || loaded.Proxies[0].TargetRef != "hhdev" {
		t.Fatalf("代理引用未正确持久化: %+v", loaded.Proxies)
	}
	if len(loaded.Targets[0].ExtraArgs) != 2 {
		t.Fatalf("额外参数丢失: %+v", loaded.Targets[0].ExtraArgs)
	}
}
