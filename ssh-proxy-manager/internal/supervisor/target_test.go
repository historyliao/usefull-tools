package supervisor_test

import (
	"path/filepath"
	"testing"

	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/config"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/supervisor"
)

func newTargetManager(t *testing.T) *supervisor.Manager {
	t.Helper()
	dir := t.TempDir()
	mgr, err := supervisor.New(
		filepath.Join(dir, "config.json"),
		filepath.Join(dir, "state.json"),
		filepath.Join(dir, "events.jsonl"),
		filepath.Join(dir, "logs"),
		"/bin/echo",
	)
	if err != nil {
		t.Fatal(err)
	}
	return mgr
}

func refDefinition(name, ref string) config.Definition {
	return config.Normalize(config.Definition{
		Name:      name,
		Direction: config.DirectionRev,
		TargetRef: ref,
		Forwards: []config.Forward{
			{Type: "R", BindHost: "127.0.0.1", BindPort: 9100, DestHost: "10.0.0.1", DestPort: 80},
		},
		Restart: config.RestartOnFail,
		Backoff: config.DefaultBackoff(),
	})
}

func TestManagerTargetLifecycle(t *testing.T) {
	mgr := newTargetManager(t)

	if err := mgr.AddTarget(config.SSHTarget{Name: "hhdev", User: "lyy", Host: "example.com", Port: 12880}); err != nil {
		t.Fatalf("新增目标失败: %v", err)
	}
	if err := mgr.AddTarget(config.SSHTarget{Name: "hhdev", User: "lyy", Host: "example.com", Port: 12880}); err == nil {
		t.Fatal("同名目标应拒绝重复新增")
	}

	if err := mgr.AddDefinition(refDefinition("p1", "hhdev")); err != nil {
		t.Fatalf("引用已存在目标应可新增代理: %v", err)
	}
	if err := mgr.AddDefinition(refDefinition("p2", "missing")); err == nil {
		t.Fatal("引用不存在的目标应拒绝")
	}
	if err := mgr.DeleteTarget("hhdev"); err == nil {
		t.Fatal("目标被引用时应拒绝删除")
	}

	if err := mgr.UpdateTarget(config.SSHTarget{Name: "hhdev", User: "lyy", Host: "example.org", Port: 12881}); err != nil {
		t.Fatalf("更新目标失败: %v", err)
	}
	if err := mgr.DeleteDefinition("p1"); err != nil {
		t.Fatalf("删除代理失败: %v", err)
	}
	if err := mgr.DeleteTarget("hhdev"); err != nil {
		t.Fatalf("无引用时应可删除目标: %v", err)
	}
	if targets := mgr.Targets(); len(targets) != 0 {
		t.Fatalf("目标应已清空: %+v", targets)
	}
}
