package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/config"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/guard"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/logging"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/store"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/supervisor"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/tui"
)

const version = "0.1.0"

type paths struct {
	dir    string
	cfg    string
	state  string
	events string
	logs   string
	lock   string
}

func resolvePaths(dir string) paths {
	if dir == "" {
		dir = os.Getenv("SPM_DIR")
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir = filepath.Join(home, ".ssh-proxy-manager")
	}
	if strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~/"))
		}
	}
	return paths{
		dir:    dir,
		cfg:    filepath.Join(dir, "config.json"),
		state:  filepath.Join(dir, "state.json"),
		events: filepath.Join(dir, "events.jsonl"),
		logs:   filepath.Join(dir, "logs"),
		lock:   filepath.Join(dir, "lock"),
	}
}

func sshBin() string {
	if bin := os.Getenv("SPM_SSH_BIN"); bin != "" {
		return bin
	}
	return "ssh"
}

func main() {
	if len(os.Args) < 2 {
		os.Exit(runTUI(""))
	}
	switch os.Args[1] {
	case "tui":
		os.Exit(runTUI(flagDir(os.Args[2:])))
	case "run":
		os.Exit(runHeadless(flagDir(os.Args[2:])))
	case "create", "add":
		os.Exit(cmdCreate(os.Args[2:]))
	case "list", "ls":
		os.Exit(cmdList(os.Args[2:]))
	case "delete", "rm", "remove":
		os.Exit(cmdDelete(os.Args[2:]))
	case "logs":
		os.Exit(cmdLogs(os.Args[2:]))
	case "__guard":
		os.Exit(cmdGuard(os.Args[2:]))
	case "version", "--version", "-v":
		fmt.Printf("spm %s\n", version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Print(`spm - ssh 隧道管理器（TUI 即 manager，退出即收走所有隧道）

用法:
  spm                              打开 TUI（manager 本体）
  spm run                          无界面 supervisor，便于脚本化与排障
  spm create --name N --ssh U@H[:P] -R bind:port:dest:port ...
  spm list [--json]                查看定义与运行态
  spm delete <name>                删除定义
  spm logs <name> [-f]             查看隧道日志

通用参数:
  --dir <path>                     状态目录，默认 ~/.ssh-proxy-manager

环境变量:
  SPM_DIR            状态目录
  SPM_SSH_BIN        替换 ssh 可执行文件（测试用）
`)
}

func flagDir(args []string) string {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--dir" && i+1 < len(args):
			return args[i+1]
		case strings.HasPrefix(args[i], "--dir="):
			return strings.TrimPrefix(args[i], "--dir=")
		}
	}
	return ""
}

func exitWithError(err error) int {
	fmt.Fprintf(os.Stderr, "错误: %v\n", err)
	return 1
}

func newManager(p paths) (*supervisor.Manager, error) {
	return supervisor.New(p.cfg, p.state, p.events, p.logs, sshBin())
}

func cmdGuard(args []string) int {
	name := ""
	var argv []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--name" && i+1 < len(args):
			name = args[i+1]
			i++
		case args[i] == "--":
			argv = args[i+1:]
			i = len(args)
		}
	}
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "guard: 缺少 -- 之后的命令")
		return 2
	}
	return guard.Run(guard.Options{Name: name, Argv: argv})
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func cmdCreate(args []string) int {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	dir := fs.String("dir", "", "状态目录")
	name := fs.String("name", "", "隧道名称")
	sshTarget := fs.String("ssh", "", "目标，形如 user@host:port")
	identity := fs.String("identity", "", "私钥路径")
	restart := fs.String("restart", config.RestartOnFail, "重启策略: always/on-failure/never")
	autostart := fs.Bool("autostart", true, "manager 启动时自动拉起")
	var forwards, forwardR, forwardL, forwardD, extraArgs multiFlag
	fs.Var(&forwards, "forward", "转发规则，如 \"R 127.0.0.1:9000:192.168.179.3:9000\"（可重复）")
	fs.Var(&forwardR, "R", "反向转发 bind:port:dest:port（可重复）")
	fs.Var(&forwardL, "L", "本地转发 bind:port:dest:port（可重复）")
	fs.Var(&forwardD, "D", "动态 SOCKS 转发 bind:port（可重复）")
	fs.Var(&extraArgs, "extra-arg", "透传给 ssh 的额外参数（可重复，每次一个参数）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *name == "" || *sshTarget == "" {
		fmt.Fprintln(os.Stderr, "错误: --name 与 --ssh 必填")
		return 2
	}
	target, err := config.ParseTarget(*sshTarget)
	if err != nil {
		return exitWithError(err)
	}
	target.Identity = *identity
	target.ExtraArgs = []string(extraArgs)

	def := config.Definition{
		Name:      *name,
		Target:    target,
		Restart:   *restart,
		Backoff:   config.DefaultBackoff(),
		Autostart: *autostart,
	}
	for _, expr := range forwards {
		f, err := config.ParseForwardExpr(expr)
		if err != nil {
			return exitWithError(err)
		}
		def.Forwards = append(def.Forwards, f)
	}
	for _, spec := range forwardR {
		f, err := config.ParseForward("R", spec)
		if err != nil {
			return exitWithError(err)
		}
		def.Forwards = append(def.Forwards, f)
	}
	for _, spec := range forwardL {
		f, err := config.ParseForward("L", spec)
		if err != nil {
			return exitWithError(err)
		}
		def.Forwards = append(def.Forwards, f)
	}
	for _, spec := range forwardD {
		f, err := config.ParseForward("D", spec)
		if err != nil {
			return exitWithError(err)
		}
		def.Forwards = append(def.Forwards, f)
	}

	mgr, err := newManager(resolvePaths(*dir))
	if err != nil {
		return exitWithError(err)
	}
	if err := mgr.AddDefinition(def); err != nil {
		return exitWithError(err)
	}
	fmt.Printf("已写入定义 %s（%s，%s）。启动请运行 spm，隧道生命周期跟随 manager。\n", def.Name, def.Target.Address(), def.ForwardsDisplay())
	return 0
}

func cmdList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	dir := fs.String("dir", "", "状态目录")
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	p := resolvePaths(*dir)
	rows, err := supervisor.LoadRows(p.cfg, p.state)
	if err != nil {
		return exitWithError(err)
	}
	if *asJSON {
		type item struct {
			Name       string            `json:"name"`
			Target     string            `json:"target"`
			Forwards   []string          `json:"forwards"`
			State      string            `json:"state"`
			PID        int               `json:"pid,omitempty"`
			Restarts   int               `json:"restarts"`
			LastError  string            `json:"last_error,omitempty"`
			Definition config.Definition `json:"definition"`
		}
		items := make([]item, 0, len(rows))
		for _, row := range rows {
			forwards := make([]string, 0, len(row.Definition.Forwards))
			for _, f := range row.Definition.Forwards {
				forwards = append(forwards, f.Display())
			}
			items = append(items, item{
				Name:       row.Definition.Name,
				Target:     row.Definition.Target.Address(),
				Forwards:   forwards,
				State:      row.Runtime.State,
				PID:        row.Runtime.PID,
				Restarts:   row.Runtime.Restarts,
				LastError:  row.Runtime.LastError,
				Definition: row.Definition,
			})
		}
		data, err := json.MarshalIndent(items, "", "  ")
		if err != nil {
			return exitWithError(err)
		}
		fmt.Println(string(data))
		return 0
	}
	if len(rows) == 0 {
		fmt.Println("暂无隧道定义，运行 spm 进入 TUI 新建。")
		return 0
	}
	fmt.Printf("%-20s %-26s %-38s %-11s %-7s %-5s %s\n", "NAME", "TARGET", "FORWARDS", "STATE", "PID", "RST", "LAST ERROR")
	for _, row := range rows {
		fmt.Printf("%-20s %-26s %-38s %-11s %-7d %-5d %s\n",
			row.Definition.Name,
			row.Definition.Target.Address(),
			truncate(row.Definition.ForwardsDisplay(), 38),
			row.Runtime.State,
			row.Runtime.PID,
			row.Runtime.Restarts,
			truncate(row.Runtime.LastError, 40),
		)
	}
	return 0
}

func cmdDelete(args []string) int {
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	dir := fs.String("dir", "", "状态目录")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "用法: spm delete <name>")
		return 2
	}
	mgr, err := newManager(resolvePaths(*dir))
	if err != nil {
		return exitWithError(err)
	}
	if err := mgr.DeleteDefinition(rest[0]); err != nil {
		return exitWithError(err)
	}
	fmt.Printf("已删除定义 %s。\n", rest[0])
	return 0
}

func cmdLogs(args []string) int {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	dir := fs.String("dir", "", "状态目录")
	follow := fs.Bool("f", false, "持续输出")
	lines := fs.Int("n", 200, "输出末尾行数")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "用法: spm logs <name> [-f]")
		return 2
	}
	p := resolvePaths(*dir)
	path := logging.Path(p.logs, rest[0])
	if *follow {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
		defer stop()
		return exitWithError(logging.Follow(ctx, path, os.Stdout))
	}
	tail, err := logging.Tail(path, *lines)
	if err != nil {
		return exitWithError(err)
	}
	for _, line := range tail {
		fmt.Println(line)
	}
	return 0
}

func runHeadless(dir string) int {
	p := resolvePaths(dir)
	lock, err := store.AcquireLock(p.lock)
	if err != nil {
		return exitWithError(err)
	}
	defer lock.Release()

	mgr, err := newManager(p)
	if err != nil {
		return exitWithError(err)
	}
	mgr.SetEmitter(func(ev store.Event) {
		fmt.Printf("%s [%s] %s %s\n", ev.Time.Format("15:04:05"), ev.Kind, ev.Name, ev.Message)
	})
	if cleaned := mgr.Reconcile(); len(cleaned) > 0 {
		fmt.Printf("已回收上一轮残留: %s\n", strings.Join(cleaned, ", "))
	}
	for _, err := range mgr.StartAutostart() {
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer stop()
	fmt.Println("supervisor 已启动，Ctrl-C 退出（退出会关闭所有隧道）")
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				added, removed, changed := mgr.ReloadDefinitions()
				for _, name := range added {
					fmt.Printf("配置同步: 新增 %s（未自动启动）\n", name)
				}
				for _, name := range changed {
					fmt.Printf("配置同步: %s 定义已更新\n", name)
				}
				for _, name := range removed {
					fmt.Printf("配置同步: %s 已从配置移除并停止\n", name)
				}
			}
		}
	}()
	<-ctx.Done()
	fmt.Println("正在关闭所有隧道…")
	mgr.Shutdown()
	fmt.Println("已全部关闭")
	return 0
}

func runTUI(dir string) int {
	p := resolvePaths(dir)
	lock, err := store.AcquireLock(p.lock)
	if err != nil {
		return exitWithError(err)
	}
	defer lock.Release()

	mgr, err := newManager(p)
	if err != nil {
		return exitWithError(err)
	}
	reclaimed := mgr.Reconcile()
	if len(reclaimed) > 0 {
		fmt.Printf("已回收上一轮残留: %s\n", strings.Join(reclaimed, ", "))
	}
	for _, err := range mgr.StartAutostart() {
		fmt.Fprintf(os.Stderr, "自动启动失败: %v\n", err)
	}
	defer mgr.Shutdown()
	return tui.Run(tui.Options{
		Manager:  mgr,
		LogDir:   p.logs,
		StateDir: p.dir,
		Version:  version,
	})
}

func truncate(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	if limit < 2 {
		return s[:limit]
	}
	return s[:limit-1] + "…"
}
