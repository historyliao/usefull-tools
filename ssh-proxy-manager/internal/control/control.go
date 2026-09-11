package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/config"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/logging"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/store"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/supervisor"
)

const (
	SocketName  = "control.sock"
	callTimeout = 60 * time.Second
)

func SocketPath(dir string) string {
	return filepath.Join(dir, SocketName)
}

type Request struct {
	Cmd        string             `json:"cmd"`
	Name       string             `json:"name,omitempty"`
	Lines      int                `json:"lines,omitempty"`
	Definition *config.Definition `json:"definition,omitempty"`
	Target     *config.SSHTarget  `json:"target,omitempty"`
}

type Row struct {
	Definition config.Definition `json:"definition"`
	Runtime    store.Runtime     `json:"runtime"`
}

type Response struct {
	OK         bool               `json:"ok"`
	Error      string             `json:"error,omitempty"`
	Message    string             `json:"message,omitempty"`
	Version    string             `json:"version,omitempty"`
	ManagerPID int                `json:"manager_pid,omitempty"`
	Rows       []Row              `json:"rows,omitempty"`
	Targets    []config.SSHTarget `json:"targets,omitempty"`
	Logs       []string           `json:"logs,omitempty"`
}

type Server struct {
	path     string
	listener net.Listener
	mgr      *supervisor.Manager
	version  string
	closed   chan struct{}
}

// Serve 在 dir 下监听控制通道；manager 进程退出时由 Close 清理 socket 文件。
func Serve(dir string, mgr *supervisor.Manager, version string) (*Server, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := SocketPath(dir)
	if len(path) > 100 {
		return nil, fmt.Errorf("控制通道路径过长（unix socket 上限 104 字节）: %s", path)
	}
	if conn, err := net.DialTimeout("unix", path, 300*time.Millisecond); err == nil {
		_ = conn.Close()
		return nil, fmt.Errorf("控制通道 %s 已被占用", path)
	}
	_ = os.Remove(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	srv := &Server{path: path, listener: listener, mgr: mgr, version: version, closed: make(chan struct{})}
	go srv.loop()
	return srv, nil
}

func (s *Server) Path() string {
	return s.path
}

func (s *Server) Close() {
	select {
	case <-s.closed:
		return
	default:
	}
	close(s.closed)
	_ = s.listener.Close()
	_ = os.Remove(s.path)
}

func (s *Server) loop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.closed:
				return
			default:
			}
			continue
		}
		go func() {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(callTimeout))
			var req Request
			if err := json.NewDecoder(conn).Decode(&req); err != nil {
				_ = json.NewEncoder(conn).Encode(Response{Error: fmt.Sprintf("请求解析失败: %v", err)})
				return
			}
			_ = json.NewEncoder(conn).Encode(s.handle(req))
		}()
	}
}

func (s *Server) handle(req Request) Response {
	switch strings.ToLower(strings.TrimSpace(req.Cmd)) {
	case "ping":
		return Response{OK: true, Version: s.version, ManagerPID: os.Getpid()}
	case "list":
		rows := make([]Row, 0)
		for _, row := range s.mgr.Rows() {
			rows = append(rows, Row{Definition: row.Definition, Runtime: row.Runtime})
		}
		return Response{OK: true, Rows: rows}
	case "logs":
		if req.Name == "" {
			return Response{Error: "缺少 name"}
		}
		lines := req.Lines
		if lines <= 0 {
			lines = 200
		}
		tail, err := logging.Tail(s.mgr.LogPath(req.Name), lines)
		if err != nil {
			return Response{Error: err.Error()}
		}
		return Response{OK: true, Logs: tail}
	case "start", "stop", "restart", "delete":
		if req.Name == "" {
			return Response{Error: "缺少 name"}
		}
		if err := s.operate(req.Cmd, req.Name); err != nil {
			return Response{Error: err.Error()}
		}
		return Response{OK: true, Message: fmt.Sprintf("%s 已%s", req.Name, actionLabel(req.Cmd))}
	case "add", "update":
		if req.Definition == nil {
			return Response{Error: "缺少 definition"}
		}
		var err error
		if req.Cmd == "add" {
			err = s.mgr.AddDefinition(*req.Definition)
		} else {
			err = s.mgr.UpdateDefinition(*req.Definition)
		}
		if err != nil {
			return Response{Error: err.Error()}
		}
		return Response{OK: true, Message: fmt.Sprintf("%s 已保存", req.Definition.Name)}
	case "targets":
		return Response{OK: true, Targets: s.mgr.Targets()}
	case "target-add", "target-update":
		if req.Target == nil {
			return Response{Error: "缺少 target"}
		}
		var err error
		if req.Cmd == "target-add" {
			err = s.mgr.AddTarget(*req.Target)
		} else {
			err = s.mgr.UpdateTarget(*req.Target)
		}
		if err != nil {
			return Response{Error: err.Error()}
		}
		return Response{OK: true, Message: fmt.Sprintf("SSH 目标 %s 已保存", req.Target.Name)}
	case "target-delete":
		if req.Name == "" {
			return Response{Error: "缺少 name"}
		}
		if err := s.mgr.DeleteTarget(req.Name); err != nil {
			return Response{Error: err.Error()}
		}
		return Response{OK: true, Message: fmt.Sprintf("SSH 目标 %s 已删除", req.Name)}
	case "shutdown":
		go s.mgr.Shutdown()
		return Response{OK: true, Message: "manager 正在退出"}
	default:
		return Response{Error: fmt.Sprintf("未知命令 %q", req.Cmd)}
	}
}

func (s *Server) operate(cmd, name string) error {
	switch cmd {
	case "start":
		return s.mgr.Start(name)
	case "stop":
		return s.mgr.Stop(name)
	case "restart":
		return s.mgr.Restart(name)
	default:
		return s.mgr.DeleteDefinition(name)
	}
}

func actionLabel(cmd string) string {
	switch cmd {
	case "start":
		return "启动"
	case "stop":
		return "停止"
	case "restart":
		return "重启"
	default:
		return "删除"
	}
}

func Call(path string, req Request) (Response, error) {
	conn, err := net.DialTimeout("unix", path, 3*time.Second)
	if err != nil {
		return Response{}, fmt.Errorf("manager 未运行（控制通道 %s 不可用）", path)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(callTimeout))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return Response{}, err
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "操作失败"
		}
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}
