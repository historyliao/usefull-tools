package supervisor

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/config"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/logging"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/sshcmd"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/store"
)

const (
	gracePeriod        = 5 * time.Second
	stableResetWindow  = 30 * time.Second
	spawnStatusTimeout = 5 * time.Second
	defaultSSHBin      = "ssh"
)

type Row struct {
	Definition config.Definition
	Runtime    store.Runtime
}

type instance struct {
	def       config.Definition
	runtime   store.Runtime
	guard     *exec.Cmd
	done      chan struct{}
	deathW    *os.File
	gen       int
	attempts  int
	startedAt time.Time
}

type Manager struct {
	cfgPath     string
	statePath   string
	eventsPath  string
	logDir      string
	sshBin      string
	selfPath    string
	stableAfter time.Duration

	mu        sync.Mutex
	cfg       *config.Config
	instances map[string]*instance
	events    chan store.Event
	emitter   func(store.Event)
	closing   bool
}

func New(cfgPath, statePath, eventsPath, logDir, sshBin string) (*Manager, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	if sshBin == "" {
		sshBin = defaultSSHBin
	}
	selfPath := os.Getenv("SPM_SELF")
	if selfPath == "" {
		selfPath, _ = os.Executable()
	}
	m := &Manager{
		cfgPath:     cfgPath,
		statePath:   statePath,
		eventsPath:  eventsPath,
		logDir:      logDir,
		sshBin:      sshBin,
		selfPath:    selfPath,
		stableAfter: stableResetWindow,
		cfg:         cfg,
		instances:   map[string]*instance{},
		events:      make(chan store.Event, 256),
	}
	for _, def := range cfg.Proxies {
		m.instances[def.Name] = &instance{
			def:     def,
			runtime: store.Runtime{Name: def.Name, State: store.StateStopped},
		}
	}
	return m, nil
}

func (m *Manager) SetEmitter(fn func(store.Event)) {
	m.mu.Lock()
	m.emitter = fn
	m.mu.Unlock()
}

func (m *Manager) Events() <-chan store.Event {
	return m.events
}

func (m *Manager) LogPath(name string) string {
	return logging.Path(m.logDir, name)
}

func (m *Manager) Definitions() []config.Definition {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.definitionsLocked()
}

func (m *Manager) definitionsLocked() []config.Definition {
	out := make([]config.Definition, 0, len(m.instances))
	for _, inst := range m.instances {
		out = append(out, inst.def)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *Manager) Rows() []Row {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]Row, 0, len(m.instances))
	for _, inst := range m.instances {
		rows = append(rows, Row{Definition: inst.def, Runtime: inst.runtime})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Definition.Name < rows[j].Definition.Name })
	return rows
}

func (m *Manager) Definition(name string) (config.Definition, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[name]
	if !ok {
		return config.Definition{}, false
	}
	return inst.def, true
}

func (m *Manager) AddDefinition(def config.Definition) error {
	def = config.Normalize(def)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.cfg.ValidateDefinition(def); err != nil {
		return err
	}
	if _, ok := m.instances[def.Name]; ok {
		return fmt.Errorf("隧道 %q 已存在", def.Name)
	}
	if err := m.conflictLocked(def); err != nil {
		return err
	}
	m.cfg.Proxies = append(m.cfg.Proxies, def)
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.cfg.Remove(def.Name)
		return err
	}
	m.instances[def.Name] = &instance{
		def:     def,
		runtime: store.Runtime{Name: def.Name, State: store.StateStopped},
	}
	return nil
}

func (m *Manager) UpdateDefinition(def config.Definition) error {
	def = config.Normalize(def)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.cfg.ValidateDefinition(def); err != nil {
		return err
	}
	inst, ok := m.instances[def.Name]
	if !ok {
		return fmt.Errorf("隧道 %q 不存在", def.Name)
	}
	if err := m.conflictLockedExcept(def); err != nil {
		return err
	}
	idx := m.cfg.Index(def.Name)
	if idx < 0 {
		return fmt.Errorf("隧道 %q 不在配置文件中", def.Name)
	}
	previous := m.cfg.Proxies[idx]
	m.cfg.Proxies[idx] = def
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.cfg.Proxies[idx] = previous
		return err
	}
	inst.def = def
	return nil
}

func (m *Manager) DeleteDefinition(name string) error {
	m.mu.Lock()
	_, ok := m.instances[name]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("隧道 %q 不存在", name)
	}
	if err := m.Stop(name); err != nil && !errors.Is(err, errNotRunning) {
		return err
	}
	m.mu.Lock()
	if !m.cfg.Remove(name) {
		m.mu.Unlock()
		return fmt.Errorf("隧道 %q 不在配置文件中", name)
	}
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.mu.Unlock()
		return err
	}
	delete(m.instances, name)
	m.mu.Unlock()
	m.persist()
	return nil
}

// Targets 返回当前配置里的可复用 SSH 目标（按名称排序）。
func (m *Manager) Targets() []config.SSHTarget {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]config.SSHTarget, len(m.cfg.Targets))
	copy(out, m.cfg.Targets)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *Manager) AddTarget(target config.SSHTarget) error {
	target = config.NormalizeSSHTarget(target)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cfg.FindTarget(target.Name); ok {
		return fmt.Errorf("SSH 目标 %q 已存在", target.Name)
	}
	previous := append([]config.SSHTarget(nil), m.cfg.Targets...)
	if err := m.cfg.UpsertTarget(target); err != nil {
		return err
	}
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.cfg.Targets = previous
		return err
	}
	return nil
}

func (m *Manager) UpdateTarget(target config.SSHTarget) error {
	target = config.NormalizeSSHTarget(target)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cfg.FindTarget(target.Name); !ok {
		return fmt.Errorf("SSH 目标 %q 不存在", target.Name)
	}
	previous := append([]config.SSHTarget(nil), m.cfg.Targets...)
	if err := m.cfg.UpsertTarget(target); err != nil {
		return err
	}
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.cfg.Targets = previous
		return err
	}
	return nil
}

func (m *Manager) DeleteTarget(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := append([]config.SSHTarget(nil), m.cfg.Targets...)
	if err := m.cfg.RemoveTarget(name); err != nil {
		return err
	}
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.cfg.Targets = previous
		return err
	}
	return nil
}

func (m *Manager) TargetLabel(def config.Definition) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.TargetLabel(def)
}

func (m *Manager) conflictLocked(def config.Definition) error {
	for _, other := range m.instances {
		if err := conflictBetween(def, other.def); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) conflictLockedExcept(def config.Definition) error {
	for name, other := range m.instances {
		if name == def.Name {
			continue
		}
		if err := conflictBetween(def, other.def); err != nil {
			return err
		}
	}
	return nil
}

func conflictBetween(a, b config.Definition) error {
	for _, fa := range a.Forwards {
		for _, fb := range b.Forwards {
			switch {
			case fa.Type == "R" && fb.Type == "R":
				if a.Target.Address() != b.Target.Address() {
					continue
				}
				if fa.BindHost == fb.BindHost && fa.BindPort == fb.BindPort {
					return fmt.Errorf("%s 与 %s 在远端 %s 上都占用 %s:%d", a.Name, b.Name, a.Target.Address(), fa.BindHost, fa.BindPort)
				}
			case fa.Type != "R" && fb.Type != "R":
				if fa.BindHost == fb.BindHost && fa.BindPort == fb.BindPort {
					return fmt.Errorf("%s 与 %s 都占用本地 %s:%d", a.Name, b.Name, fa.BindHost, fa.BindPort)
				}
			}
		}
	}
	return nil
}

var errNotRunning = errors.New("未运行")

func (m *Manager) Start(name string) error {
	m.mu.Lock()
	inst, ok := m.instances[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("隧道 %q 不存在", name)
	}
	if m.closing {
		m.mu.Unlock()
		return fmt.Errorf("manager 正在退出")
	}
	switch inst.runtime.State {
	case store.StateRunning, store.StateStarting, store.StateRestarting, store.StateStopping:
		state := inst.runtime.State
		m.mu.Unlock()
		return fmt.Errorf("隧道 %q 当前状态为 %s", name, state)
	}
	inst.gen++
	gen := inst.gen
	inst.attempts = 0
	m.mu.Unlock()
	return m.spawn(inst, gen)
}

func (m *Manager) StartAll() []error {
	var errs []error
	for _, def := range m.Definitions() {
		if err := m.Start(def.Name); err != nil && !isBusy(err) {
			errs = append(errs, err)
		}
	}
	return errs
}

func (m *Manager) StartAutostart() []error {
	var errs []error
	for _, def := range m.Definitions() {
		if !def.Autostart {
			continue
		}
		if err := m.Start(def.Name); err != nil && !isBusy(err) {
			errs = append(errs, err)
		}
	}
	return errs
}

func (m *Manager) Stop(name string) error {
	m.mu.Lock()
	inst, ok := m.instances[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("隧道 %q 不存在", name)
	}
	active := stateActive(inst.runtime.State) || inst.guard != nil
	if !active {
		inst.gen++
		inst.runtime.State = store.StateStopped
		inst.runtime.PID = 0
		inst.runtime.GuardPID = 0
		m.mu.Unlock()
		return errNotRunning
	}
	inst.gen++
	inst.attempts = 0
	guard := inst.guard
	guardPID := inst.runtime.GuardPID
	sshPID := inst.runtime.PID
	done := inst.done
	deathW := inst.deathW
	inst.guard = nil
	inst.done = nil
	inst.deathW = nil
	inst.runtime.State = store.StateStopping
	m.mu.Unlock()
	m.persist()

	if guard != nil && guardPID > 0 {
		_ = syscall.Kill(-guardPID, syscall.SIGTERM)
		if !waitDone(done, 6*time.Second) && processAlive(guardPID) {
			_ = syscall.Kill(-guardPID, syscall.SIGKILL)
			waitGone(guardPID, 2*time.Second)
		}
	}
	if sshPID > 0 && processAlive(sshPID) {
		_ = syscall.Kill(-sshPID, syscall.SIGTERM)
		if !waitGone(sshPID, 3*time.Second) {
			_ = syscall.Kill(-sshPID, syscall.SIGKILL)
			waitGone(sshPID, time.Second)
		}
	}
	if deathW != nil {
		_ = deathW.Close()
	}

	m.mu.Lock()
	inst.runtime.State = store.StateStopped
	inst.runtime.PID = 0
	inst.runtime.GuardPID = 0
	inst.runtime.ProcStart = ""
	inst.runtime.ProcCmdline = ""
	inst.runtime.GuardStart = ""
	inst.runtime.GuardCmdline = ""
	inst.runtime.StartedAt = time.Time{}
	m.mu.Unlock()
	m.emit(store.Event{Name: name, Kind: "stop"})
	m.persist()
	return nil
}

func (m *Manager) Restart(name string) error {
	if err := m.Stop(name); err != nil && !errors.Is(err, errNotRunning) {
		return err
	}
	return m.Start(name)
}

func (m *Manager) StopAll() []error {
	var errs []error
	for _, def := range m.Definitions() {
		if err := m.Stop(def.Name); err != nil && !errors.Is(err, errNotRunning) {
			errs = append(errs, err)
		}
	}
	return errs
}

func (m *Manager) Shutdown() {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return
	}
	m.closing = true
	names := make([]string, 0, len(m.instances))
	for name, inst := range m.instances {
		if stateActive(inst.runtime.State) || inst.guard != nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	m.mu.Unlock()

	for _, name := range names {
		_ = m.Stop(name)
	}
	m.persist()
}

func (m *Manager) Reconcile() []string {
	runtimes, err := store.LoadState(m.statePath)
	if err != nil {
		return nil
	}
	var cleaned []string
	for name, rt := range runtimes {
		if !stateActive(rt.State) {
			continue
		}
		guardKilled := killRecorded(rt.GuardPID, rt.GuardStart, rt.GuardCmdline)
		sshKilled := killRecorded(rt.PID, rt.ProcStart, rt.ProcCmdline)
		if guardKilled || sshKilled {
			cleaned = append(cleaned, name)
		}
	}
	sort.Strings(cleaned)
	if len(cleaned) == 0 {
		return nil
	}
	m.mu.Lock()
	for _, name := range cleaned {
		if inst, ok := m.instances[name]; ok {
			inst.runtime.State = store.StateStopped
			inst.runtime.PID = 0
			inst.runtime.GuardPID = 0
			inst.runtime.ProcStart = ""
			inst.runtime.ProcCmdline = ""
			inst.runtime.GuardStart = ""
			inst.runtime.GuardCmdline = ""
			inst.runtime.LastError = "上一轮 manager 退出后残留，已回收"
		}
	}
	m.mu.Unlock()
	m.persist()
	return cleaned
}

// ReloadDefinitions 让长期运行的 manager 收敛外部对 config.json 的改动
// （例如另一个终端执行 spm create/delete）。
func (m *Manager) ReloadDefinitions() (added, removed, changed []string) {
	cfg, err := config.Load(m.cfgPath)
	if err != nil {
		return nil, nil, nil
	}
	m.mu.Lock()
	for name, inst := range m.instances {
		def, ok := cfg.Find(name)
		if !ok {
			removed = append(removed, name)
			continue
		}
		def = config.Normalize(def)
		if !reflect.DeepEqual(def, inst.def) {
			changed = append(changed, name)
			inst.def = def
			if stateActive(inst.runtime.State) {
				inst.runtime.LastError = "定义已更新，需重启生效"
			}
		}
	}
	for _, def := range cfg.Proxies {
		def = config.Normalize(def)
		if _, ok := m.instances[def.Name]; !ok {
			added = append(added, def.Name)
			m.instances[def.Name] = &instance{
				def:     def,
				runtime: store.Runtime{Name: def.Name, State: store.StateStopped},
			}
		}
	}
	m.cfg = cfg
	m.mu.Unlock()

	sort.Strings(added)
	sort.Strings(changed)
	sort.Strings(removed)
	for _, name := range removed {
		_ = m.Stop(name)
		m.mu.Lock()
		delete(m.instances, name)
		m.mu.Unlock()
	}
	if len(added)+len(removed)+len(changed) > 0 {
		m.persist()
	}
	return added, removed, changed
}

func (m *Manager) spawn(inst *instance, gen int) error {
	m.mu.Lock()
	if m.closing || inst.gen != gen {
		m.mu.Unlock()
		return nil
	}
	def := inst.def
	target, err := m.cfg.ResolveTarget(def)
	if err != nil {
		m.mu.Unlock()
		return m.failSpawn(inst, gen, err)
	}
	resolved := def
	resolved.Target = target
	inst.runtime.State = store.StateStarting
	if inst.attempts > 0 {
		inst.runtime.Restarts++
	}
	m.mu.Unlock()
	m.persist()

	execPath := m.selfPath
	if execPath == "" {
		return m.failSpawn(inst, gen, errors.New("找不到 spm 可执行文件"))
	}
	logPath := m.LogPath(def.Name)
	if err := os.MkdirAll(m.logDir, 0o700); err != nil {
		return m.failSpawn(inst, gen, err)
	}
	_ = logging.Append(logPath, fmt.Sprintf("# %s 启动: %s", time.Now().Format(time.RFC3339), strings.Join(sshcmd.Args(m.sshBin, resolved), " ")))

	deathR, deathW, err := os.Pipe()
	if err != nil {
		return m.failSpawn(inst, gen, err)
	}
	statusR, statusW, err := os.Pipe()
	if err != nil {
		_ = deathR.Close()
		_ = deathW.Close()
		return m.failSpawn(inst, gen, err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_ = deathR.Close()
		_ = deathW.Close()
		_ = statusR.Close()
		_ = statusW.Close()
		return m.failSpawn(inst, gen, err)
	}

	argv := sshcmd.Args(m.sshBin, resolved)
	guardArgv := append([]string{"__guard", "--name", def.Name, "--"}, argv...)
	cmd := exec.Command(execPath, guardArgv...)
	cmd.ExtraFiles = []*os.File{deathR, statusW}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		_ = deathR.Close()
		_ = deathW.Close()
		_ = statusR.Close()
		_ = statusW.Close()
		return m.failSpawn(inst, gen, err)
	}
	_ = logFile.Close()
	_ = deathR.Close()
	_ = statusW.Close()
	sshPID := readStatusPID(statusR, spawnStatusTimeout)
	_ = statusR.Close()

	m.mu.Lock()
	if m.closing || inst.gen != gen {
		m.mu.Unlock()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		_ = deathW.Close()
		return nil
	}
	done := make(chan struct{})
	inst.guard = cmd
	inst.done = done
	inst.deathW = deathW
	inst.startedAt = time.Now()
	inst.runtime.State = store.StateRunning
	inst.runtime.GuardPID = cmd.Process.Pid
	inst.runtime.GuardStart = startTimeOf(cmd.Process.Pid)
	inst.runtime.GuardCmdline = cmdlineOf(cmd.Process.Pid)
	inst.runtime.PID = sshPID
	inst.runtime.ProcStart = startTimeOf(sshPID)
	inst.runtime.ProcCmdline = cmdlineOf(sshPID)
	inst.runtime.StartedAt = inst.startedAt
	inst.runtime.LastError = ""
	message := fmt.Sprintf("pid=%d guard=%d %s", sshPID, cmd.Process.Pid, def.ForwardsDisplay())
	if sshPID == 0 {
		message = fmt.Sprintf("guard=%d（ssh pid 未知）", cmd.Process.Pid)
	}
	m.mu.Unlock()

	m.emit(store.Event{Name: def.Name, Kind: "start", Message: message})
	m.persist()
	go m.watch(inst, cmd, done, gen)
	return nil
}

func (m *Manager) failSpawn(inst *instance, gen int, err error) error {
	m.handleExit(inst, gen, -1, 0, err)
	return err
}

func (m *Manager) watch(inst *instance, cmd *exec.Cmd, done chan struct{}, gen int) {
	err := cmd.Wait()
	close(done)
	code, signal := exitStatusOf(err)
	m.handleExit(inst, gen, code, signal, err)
}

func (m *Manager) handleExit(inst *instance, gen, code, signal int, cause error) {
	m.mu.Lock()
	if inst.deathW != nil {
		_ = inst.deathW.Close()
		inst.deathW = nil
	}
	inst.guard = nil
	inst.done = nil
	if inst.gen != gen || m.closing {
		m.mu.Unlock()
		return
	}
	name := inst.def.Name
	now := time.Now()
	uptime := now.Sub(inst.startedAt)
	inst.runtime.PID = 0
	inst.runtime.GuardPID = 0
	inst.runtime.ProcStart = ""
	inst.runtime.ProcCmdline = ""
	inst.runtime.GuardStart = ""
	inst.runtime.GuardCmdline = ""
	inst.runtime.LastExitCode = &code
	inst.runtime.LastExitAt = now
	message := exitMessage(code, signal, cause)
	inst.runtime.LastError = message

	if uptime >= m.stableAfter {
		inst.attempts = 0
	}
	restartable := inst.def.Restart == config.RestartAlways ||
		(inst.def.Restart == config.RestartOnFail && (code != 0 || signal > 0 || cause != nil))
	if !restartable {
		inst.runtime.State = store.StateStopped
		m.mu.Unlock()
		m.emit(store.Event{Name: name, Kind: "exit", Message: message})
		m.persist()
		return
	}

	inst.attempts++
	if inst.attempts > inst.def.Backoff.MaxAttempts {
		inst.runtime.State = store.StateFailed
		failure := fmt.Sprintf("%s（连续失败 %d 次，已停止自动重试）", message, inst.attempts-1)
		inst.runtime.LastError = failure
		m.mu.Unlock()
		m.emit(store.Event{Name: name, Kind: "failed", Message: failure})
		m.persist()
		return
	}

	delay := backoffDelay(inst.def.Backoff, inst.attempts)
	attempt := inst.attempts
	inst.runtime.State = store.StateRestarting
	inst.gen++
	token := inst.gen
	m.mu.Unlock()

	m.emit(store.Event{Name: name, Kind: "restart", Message: fmt.Sprintf("%s，%s 后进行第 %d 次重试", message, delay, attempt)})
	m.persist()
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
		_ = m.spawn(inst, token)
	}()
}

func (m *Manager) emit(ev store.Event) {
	ev.Time = time.Now()
	_ = store.AppendEvent(m.eventsPath, ev)
	m.mu.Lock()
	emitter := m.emitter
	m.mu.Unlock()
	if emitter != nil {
		emitter(ev)
	}
	select {
	case m.events <- ev:
	default:
	}
}

func (m *Manager) persist() {
	m.mu.Lock()
	runtimes := make(map[string]store.Runtime, len(m.instances))
	for name, inst := range m.instances {
		runtimes[name] = inst.runtime
	}
	m.mu.Unlock()
	if err := store.SaveState(m.statePath, runtimes); err != nil {
		_ = logging.Append(m.LogPath("manager"), fmt.Sprintf("# %s 写入状态失败: %v", time.Now().Format(time.RFC3339), err))
	}
}

func LoadRows(cfgPath, statePath string) ([]Row, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	runtimes, err := store.LoadState(statePath)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(cfg.Proxies))
	for _, def := range cfg.Proxies {
		rt, ok := runtimes[def.Name]
		if !ok {
			rt = store.Runtime{Name: def.Name, State: store.StateStopped}
		}
		if stateActive(rt.State) && !processAlive(rt.PID) && !processAlive(rt.GuardPID) {
			rt.State = store.StateStopped
			rt.PID = 0
			rt.GuardPID = 0
		}
		rows = append(rows, Row{Definition: def, Runtime: rt})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Definition.Name < rows[j].Definition.Name })
	return rows, nil
}

func StateActive(state string) bool {
	return stateActive(state)
}

func stateActive(state string) bool {
	switch state {
	case store.StateRunning, store.StateStarting, store.StateStopping, store.StateRestarting:
		return true
	default:
		return false
	}
}

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, state := range []string{store.StateRunning, store.StateStarting, store.StateRestarting, store.StateStopping} {
		if strings.Contains(msg, state) {
			return true
		}
	}
	return false
}

func readStatusPID(file *os.File, timeout time.Duration) int {
	result := make(chan int, 1)
	go func() {
		reader := bufio.NewReader(file)
		line, _ := reader.ReadString('\n')
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			pid = 0
		}
		result <- pid
	}()
	select {
	case pid := <-result:
		return pid
	case <-time.After(timeout):
		return 0
	}
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func waitGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !processAlive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitDone(done chan struct{}, timeout time.Duration) bool {
	if done == nil {
		return false
	}
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func startTimeOf(pid int) string {
	if pid <= 0 {
		return ""
	}
	out, err := exec.Command("/bin/ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func cmdlineOf(pid int) string {
	if pid <= 0 {
		return ""
	}
	out, err := exec.Command("/bin/ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// killRecorded 只在 pid、启动时间、cmdline 三者全部对得上时才动手，
// 避免 PID 复用后误杀无关进程；旧版本 state 里没有 cmdline 时退化为两项校验。
func killRecorded(pid int, recordedStart, recordedCmdline string) bool {
	if pid <= 0 || recordedStart == "" || !processAlive(pid) {
		return false
	}
	if startTimeOf(pid) != recordedStart {
		return false
	}
	if recordedCmdline != "" && cmdlineOf(pid) != recordedCmdline {
		return false
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	if waitGone(pid, 3*time.Second) {
		return true
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	waitGone(pid, time.Second)
	return !processAlive(pid)
}

func backoffDelay(b config.Backoff, attempt int) time.Duration {
	delay := b.Initial
	if delay <= 0 {
		delay = 1
	}
	for i := 1; i < attempt; i++ {
		delay *= 2
		if b.Max > 0 && delay >= b.Max {
			delay = b.Max
			break
		}
	}
	if b.Max > 0 && delay > b.Max {
		delay = b.Max
	}
	return time.Duration(delay) * time.Second
}

func exitStatusOf(err error) (int, int) {
	if err == nil {
		return 0, 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return -1, int(status.Signal())
		}
		if code := exitErr.ExitCode(); code >= 0 {
			return code, 0
		}
	}
	return -1, 0
}

func exitMessage(code, signal int, cause error) string {
	var exitErr *exec.ExitError
	switch {
	case signal > 0:
		return fmt.Sprintf("进程被信号 %d 终止", signal)
	case code == 0:
		return "进程正常退出"
	case code < 0:
		if cause != nil && !errors.As(cause, &exitErr) {
			return cause.Error()
		}
		return "进程异常退出"
	default:
		return fmt.Sprintf("进程退出码 %d", code)
	}
}
