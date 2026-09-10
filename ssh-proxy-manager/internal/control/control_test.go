package control_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/config"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/control"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/supervisor"
)

var (
	buildOnce sync.Once
	spmPath   string
	buildErr  error
)

func buildSPM(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "spm-control-bin")
		if err != nil {
			buildErr = err
			return
		}
		spmPath = filepath.Join(dir, "spm")
		cmd := exec.Command("go", "build", "-o", spmPath, "./cmd/spm")
		cmd.Dir = "../.."
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("构建 spm 失败: %v: %s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return spmPath
}

func newManager(t *testing.T, dir, sshBin string) *supervisor.Manager {
	t.Helper()
	t.Setenv("SPM_SELF", buildSPM(t))
	mgr, err := supervisor.New(
		filepath.Join(dir, "config.json"),
		filepath.Join(dir, "state.json"),
		filepath.Join(dir, "events.jsonl"),
		filepath.Join(dir, "logs"),
		sshBin,
	)
	if err != nil {
		t.Fatal(err)
	}
	return mgr
}

func fakeSSH(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-ssh.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 3600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func reverseProxy(name string) config.Definition {
	return config.Definition{
		Name:      name,
		Direction: config.DirectionRev,
		Target:    config.Target{User: "lyy", Host: "example", Port: 12880},
		Forwards: []config.Forward{
			{Type: "R", BindHost: "127.0.0.1", BindPort: 19000, DestHost: "127.0.0.1", DestPort: 9222},
		},
		Restart: config.RestartOnFail,
		Backoff: config.Backoff{Initial: 1, Max: 2, MaxAttempts: 3},
	}
}

func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", desc)
}

// unix socket 路径有 104 字节上限，测试不能用过长的 t.TempDir()。
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "spmctl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func alive(pid int) bool {
	return pid > 0 && syscall.Kill(pid, 0) == nil
}

func runtimeOf(t *testing.T, path, name string) (config.Definition, string, int) {
	t.Helper()
	resp, err := control.Call(path, control.Request{Cmd: "list"})
	if err != nil {
		t.Fatalf("list 失败: %v", err)
	}
	for _, row := range resp.Rows {
		if row.Definition.Name == name {
			return row.Definition, row.Runtime.State, row.Runtime.PID
		}
	}
	return config.Definition{}, "", 0
}

func TestControlChannelDrivesManager(t *testing.T) {
	dir := shortTempDir(t)
	mgr := newManager(t, dir, fakeSSH(t, dir))
	t.Cleanup(mgr.Shutdown)

	srv, err := control.Serve(dir, mgr, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	path := control.SocketPath(dir)

	ping, err := control.Call(path, control.Request{Cmd: "ping"})
	if err != nil {
		t.Fatalf("ping 失败: %v", err)
	}
	if ping.ManagerPID != os.Getpid() {
		t.Fatalf("ping 返回的 pid 异常: %d", ping.ManagerPID)
	}

	def := reverseProxy("web-9223")
	if _, err := control.Call(path, control.Request{Cmd: "add", Definition: &def}); err != nil {
		t.Fatalf("add 失败: %v", err)
	}
	got, state, pid := runtimeOf(t, path, def.Name)
	if state != "stopped" || pid != 0 {
		t.Fatalf("新增后不应运行: state=%s pid=%d", state, pid)
	}
	if got.Direction != config.DirectionRev || got.DirectionLabel() != "反向" {
		t.Fatalf("方向未落库: %+v", got)
	}

	if _, err := control.Call(path, control.Request{Cmd: "start", Name: def.Name}); err != nil {
		t.Fatalf("start 失败: %v", err)
	}
	_, state, pid = runtimeOf(t, path, def.Name)
	if state != "running" || !alive(pid) {
		t.Fatalf("start 后未运行: state=%s pid=%d", state, pid)
	}
	first := pid

	logs, err := control.Call(path, control.Request{Cmd: "logs", Name: def.Name, Lines: 20})
	if err != nil {
		t.Fatalf("logs 失败: %v", err)
	}
	if len(logs.Logs) == 0 || !strings.Contains(logs.Logs[0], "启动") {
		t.Fatalf("日志内容异常: %+v", logs.Logs)
	}

	if _, err := control.Call(path, control.Request{Cmd: "restart", Name: def.Name}); err != nil {
		t.Fatalf("restart 失败: %v", err)
	}
	_, state, pid = runtimeOf(t, path, def.Name)
	if state != "running" || !alive(pid) {
		t.Fatalf("restart 后未运行: state=%s pid=%d", state, pid)
	}
	if pid == first {
		t.Fatalf("restart 后 pid 未变化: %d", pid)
	}
	second := pid

	if _, err := control.Call(path, control.Request{Cmd: "stop", Name: def.Name}); err != nil {
		t.Fatalf("stop 失败: %v", err)
	}
	waitFor(t, 5*time.Second, "stop 后进程退出", func() bool { return !alive(second) })
	if _, state, _ = runtimeOf(t, path, def.Name); state != "stopped" {
		t.Fatalf("stop 后状态应为 stopped，实际 %s", state)
	}

	if _, err := control.Call(path, control.Request{Cmd: "delete", Name: def.Name}); err != nil {
		t.Fatalf("delete 失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), def.Name) {
		t.Fatalf("delete 后配置里仍存在: %s", data)
	}

	if _, err := control.Call(path, control.Request{Cmd: "start", Name: "不存在的隧道"}); err == nil {
		t.Fatal("对不存在的隧道 start 应当报错")
	}
	if _, err := control.Call(path, control.Request{Cmd: "nope"}); err == nil {
		t.Fatal("未知命令应当报错")
	}

	srv.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Close 后 socket 未清理: %v", err)
	}
}

// 控制通道不可用时客户端要给出明确错误，而不是挂住。
func TestCallWithoutManager(t *testing.T) {
	dir := t.TempDir()
	if _, err := control.Call(control.SocketPath(dir), control.Request{Cmd: "ping"}); err == nil {
		t.Fatal("manager 未运行时应当报错")
	}
}
