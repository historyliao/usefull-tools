package guard

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

const (
	deathFD  = 3
	statusFD = 4
)

type Options struct {
	Name  string
	Argv  []string
	Grace time.Duration
}

// Run 是 supervisor 的内部模式：它自己不解释 ssh，只负责在 manager 消失时
// 把 ssh 进程组收掉，因此 stdin/stdout/stderr 全部直接继承。
func Run(opts Options) int {
	if len(opts.Argv) == 0 {
		fmt.Fprintln(os.Stderr, "guard: 缺少要执行的命令")
		return 127
	}
	grace := opts.Grace
	if grace <= 0 {
		grace = 5 * time.Second
	}

	death := openPipe(deathFD, "death-pipe")
	status := openPipe(statusFD, "status-pipe")

	cmd := exec.Command(opts.Argv[0], opts.Argv[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		reportStatus(status, 0)
		fmt.Fprintf(os.Stderr, "guard: 启动 %s 失败: %v\n", opts.Argv[0], err)
		return 127
	}
	reportStatus(status, cmd.Process.Pid)

	exitCh := make(chan int, 2)
	go func() {
		exitCh <- exitCode(cmd.Wait())
	}()

	deathCh := make(chan struct{})
	if death != nil {
		go func() {
			_, _ = io.Copy(io.Discard, death)
			close(deathCh)
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT)

	select {
	case code := <-exitCh:
		return code
	case <-deathCh:
		return terminate(cmd, grace, exitCh)
	case <-sigCh:
		return terminate(cmd, grace, exitCh)
	}
}

func terminate(cmd *exec.Cmd, grace time.Duration, exitCh <-chan int) int {
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case code := <-exitCh:
		return code
	case <-time.After(grace):
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	select {
	case code := <-exitCh:
		return code
	case <-time.After(2 * time.Second):
		return 137
	}
}

func openPipe(fd int, name string) *os.File {
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		return nil
	}
	if _, err := file.Stat(); err != nil {
		return nil
	}
	return file
}

func reportStatus(status *os.File, pid int) {
	if status == nil {
		return
	}
	_, _ = fmt.Fprintf(status, "%d\n", pid)
	_ = status.Close()
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
	}
	return 1
}
