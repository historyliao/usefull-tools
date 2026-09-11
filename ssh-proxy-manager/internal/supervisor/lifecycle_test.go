package supervisor_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/config"
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
		dir, err := os.MkdirTemp("", "spm-bin")
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

func fakeSSH(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-ssh.sh")
	script := "#!/bin/sh\nexec sleep 3600\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestManager(t *testing.T, dir, sshBin string) *supervisor.Manager {
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

func sampleDefinition(name string) config.Definition {
	return config.Definition{
		Name: name,
		Target: config.Target{
			User: "lyy",
			Host: "example",
			Port: 12880,
		},
		Forwards: []config.Forward{
			{Type: "R", BindHost: "127.0.0.1", BindPort: 9000, DestHost: "192.168.179.3", DestPort: 9000},
		},
		Restart:   config.RestartOnFail,
		Backoff:   config.Backoff{Initial: 1, Max: 2, MaxAttempts: 5},
		Autostart: true,
	}
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", desc)
}

func tunnelPID(t *testing.T, mgr *supervisor.Manager, name string) int {
	t.Helper()
	for _, row := range mgr.Rows() {
		if row.Definition.Name == name {
			return row.Runtime.PID
		}
	}
	return 0
}

// INV-1: manager 存活期间，隧道被打死后必须被自动拉起。
func TestTunnelRestartsAfterKill(t *testing.T) {
	dir := t.TempDir()
	mgr := newTestManager(t, dir, fakeSSH(t, dir))
	if err := mgr.AddDefinition(sampleDefinition("restart-me")); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Start("restart-me"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Shutdown)

	first := tunnelPID(t, mgr, "restart-me")
	if first <= 0 {
		t.Fatalf("隧道未启动，pid=%d", first)
	}
	if err := syscall.Kill(first, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 15*time.Second, "隧道被拉起", func() bool {
		pid := tunnelPID(t, mgr, "restart-me")
		return pid > 0 && pid != first && processAlive(pid)
	})

	for _, row := range mgr.Rows() {
		if row.Definition.Name == "restart-me" && row.Runtime.Restarts < 1 {
			t.Fatalf("重启计数未增加: %+v", row.Runtime)
		}
	}
}

// INV-2: manager 被 SIGKILL 后，隧道必须随之消失（靠 death pipe 兜底）。
func TestManagerKillRemovesTunnels(t *testing.T) {
	dir := t.TempDir()
	bin := buildSPM(t)
	ssh := fakeSSH(t, dir)

	run := exec.Command(bin, "create", "--name", "kill-test", "--ssh", "lyy@example:12880", "-R", "127.0.0.1:9000:192.168.179.3:9000")
	run.Env = append(os.Environ(), "SPM_DIR="+dir)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("create 失败: %v: %s", err, out)
	}

	supervisor := exec.Command(bin, "run")
	supervisor.Env = append(os.Environ(), "SPM_DIR="+dir, "SPM_SSH_BIN="+ssh)
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if supervisor.Process != nil {
			_ = supervisor.Process.Kill()
		}
	}()

	pid := 0
	waitFor(t, 15*time.Second, "隧道启动", func() bool {
		pid = findTunnelPID(t, dir, "kill-test")
		return pid > 0
	})

	if err := syscall.Kill(supervisor.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_, _ = supervisor.Process.Wait()

	waitFor(t, 15*time.Second, "隧道随 manager 一起消失", func() bool {
		return !processAlive(pid)
	})
}

// INV-2: 正常信号路径（SIGTERM）同样要收走隧道。
func TestManagerSigtermRemovesTunnels(t *testing.T) {
	dir := t.TempDir()
	bin := buildSPM(t)
	ssh := fakeSSH(t, dir)

	run := exec.Command(bin, "create", "--name", "term-test", "--ssh", "lyy@example:12880", "-R", "127.0.0.1:9001:192.168.179.3:9000")
	run.Env = append(os.Environ(), "SPM_DIR="+dir)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("create 失败: %v: %s", err, out)
	}

	sup := exec.Command(bin, "run")
	sup.Env = append(os.Environ(), "SPM_DIR="+dir, "SPM_SSH_BIN="+ssh)
	if err := sup.Start(); err != nil {
		t.Fatal(err)
	}

	pid := 0
	waitFor(t, 15*time.Second, "隧道启动", func() bool {
		pid = findTunnelPID(t, dir, "term-test")
		return pid > 0
	})

	if err := syscall.Kill(sup.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	_, _ = sup.Process.Wait()

	waitFor(t, 15*time.Second, "隧道随 SIGTERM 消失", func() bool {
		return !processAlive(pid)
	})
}

func findTunnelPID(t *testing.T, dir, name string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return 0
	}
	marker := `"` + name + `"`
	idx := strings.Index(string(data), marker)
	if idx < 0 {
		return 0
	}
	rest := string(data)[idx:]
	pidIdx := strings.Index(rest, `"pid":`)
	if pidIdx < 0 {
		return 0
	}
	rest = rest[pidIdx+len(`"pid":`):]
	end := strings.IndexAny(rest, ",\n}")
	if end < 0 {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(rest[:end]))
	if err != nil {
		return 0
	}
	return pid
}
