package supervisor

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

type sleeper struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func startSleeper(t *testing.T) *sleeper {
	t.Helper()
	cmd := exec.Command("sleep", "120")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &sleeper{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(s.done)
	}()
	pid := cmd.Process.Pid
	deadline := time.Now().Add(3 * time.Second)
	for startTimeOf(pid) == "" || cmdlineOf(pid) == "" {
		if time.Now().After(deadline) {
			t.Fatalf("读取不到进程信息: pid=%d", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
		}
	})
	return s
}

func (s *sleeper) alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return processAlive(s.cmd.Process.Pid)
	}
}

// 残留回收只在 pid、启动时间、cmdline 三者全部对得上时才动手。
func TestKillRecordedRequiresMatchingIdentity(t *testing.T) {
	proc := startSleeper(t)
	pid := proc.cmd.Process.Pid
	start, cmdline := startTimeOf(pid), cmdlineOf(pid)

	if killRecorded(pid, "Thu Jan  1 00:00:00 1970", cmdline) {
		t.Fatal("启动时间不匹配时不应判定为已回收")
	}
	if !proc.alive() {
		t.Fatal("启动时间不匹配时不应杀进程")
	}

	if killRecorded(pid, start, "sleep 999") {
		t.Fatal("cmdline 不匹配时不应判定为已回收")
	}
	if !proc.alive() {
		t.Fatal("cmdline 不匹配时不应杀进程")
	}

	if !killRecorded(pid, start, cmdline) {
		t.Fatal("三元组匹配时应判定为已回收")
	}
	deadline := time.Now().Add(3 * time.Second)
	for proc.alive() {
		if time.Now().After(deadline) {
			t.Fatal("三元组匹配后进程仍存活")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 旧 state 没记 cmdline 时退化为 pid + 启动时间两项校验。
func TestKillRecordedWithoutRecordedCmdline(t *testing.T) {
	proc := startSleeper(t)
	pid := proc.cmd.Process.Pid

	if !killRecorded(pid, startTimeOf(pid), "") {
		t.Fatal("缺少 cmdline 时应退化为两项校验并回收")
	}
	deadline := time.Now().Add(3 * time.Second)
	for proc.alive() {
		if time.Now().After(deadline) {
			t.Fatal("进程未退出")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestKillRecordedSkipsDeadProcess(t *testing.T) {
	if killRecorded(1<<30, "Thu Jan  1 00:00:00 1970", "sleep 999") {
		t.Fatal("不存在的 pid 不应判定为已回收")
	}
}
