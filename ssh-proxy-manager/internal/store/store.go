package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/config"
)

const (
	StateStopped    = "stopped"
	StateStarting   = "starting"
	StateRunning    = "running"
	StateStopping   = "stopping"
	StateRestarting = "restarting"
	StateFailed     = "failed"
)

type Runtime struct {
	Name         string    `json:"name"`
	State        string    `json:"state"`
	PID          int       `json:"pid,omitempty"`
	GuardPID     int       `json:"guard_pid,omitempty"`
	ProcStart    string    `json:"proc_start_time,omitempty"`
	GuardStart   string    `json:"guard_start_time,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	Restarts     int       `json:"restarts"`
	LastExitCode *int      `json:"last_exit_code,omitempty"`
	LastExitAt   time.Time `json:"last_exit_at,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
}

type StateFile struct {
	Runtimes map[string]Runtime `json:"runtimes"`
}

func LoadState(path string) (map[string]Runtime, error) {
	out := map[string]Runtime{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	var sf StateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, err
	}
	if sf.Runtimes != nil {
		out = sf.Runtimes
	}
	return out, nil
}

func SaveState(path string, runtimes map[string]Runtime) error {
	data, err := json.MarshalIndent(StateFile{Runtimes: runtimes}, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(path, append(data, '\n'), 0o600)
}

type Lock struct {
	file *os.File
}

func AcquireLock(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("另一个 manager 正在运行（%s 已被锁定）", path)
	}
	if err := file.Truncate(0); err == nil {
		_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
	}
	return &Lock{file: file}, nil
}

func (l *Lock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
}

type Event struct {
	Time    time.Time `json:"time"`
	Name    string    `json:"name,omitempty"`
	Kind    string    `json:"kind"`
	Message string    `json:"message,omitempty"`
}

func AppendEvent(path string, ev Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(data, '\n'))
	return err
}
