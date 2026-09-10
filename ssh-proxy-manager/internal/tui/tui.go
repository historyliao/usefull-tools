package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/config"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/logging"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/store"
	"github.com/historyliao/usefull-tools/ssh-proxy-manager/internal/supervisor"
)

type mode int

const (
	modeList mode = iota
	modeForm
	modeConfirm
	modeHelp
	modeLog
)

const logPaneLines = 9

type Options struct {
	Manager  *supervisor.Manager
	LogDir   string
	StateDir string
	Version  string
}

type field struct {
	key   string
	label string
	value string
	hint  string
	menu  []string
}

type formState struct {
	title   string
	editing bool
	name    string
	fields  []field
	focus   int
	err     string
}

type confirmState struct {
	title  string
	detail string
	onYes  tea.Cmd
}

type Model struct {
	mgr      *supervisor.Manager
	logDir   string
	stateDir string
	version  string

	rows   []supervisor.Row
	cursor int
	width  int
	height int

	mode      mode
	form      *formState
	confirm   *confirmState
	logLines  []string
	logScroll int
	filter    string
	filtering bool
	status    string
	statusErr bool
	ticks     int
}

type tickMsg time.Time

type eventMsg store.Event

type reloadMsg struct {
	added   []string
	removed []string
	changed []string
}

type opMsg struct {
	name   string
	action string
	err    error
}

func Run(opts Options) int {
	model := newModel(opts)
	program := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI 运行失败: %v\n", err)
		return 1
	}
	return 0
}

func newModel(opts Options) Model {
	m := Model{
		mgr:      opts.Manager,
		logDir:   opts.LogDir,
		stateDir: opts.StateDir,
		version:  opts.Version,
		width:    100,
		height:   30,
	}
	m.refresh()
	m.refreshLogs()
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(tickCmd(), waitEvent(m.mgr.Events()))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		m.ticks++
		m.refresh()
		m.refreshLogs()
		cmds := []tea.Cmd{tickCmd()}
		if m.ticks%5 == 0 {
			cmds = append(cmds, reloadCmd(m.mgr))
		}
		return m, tea.Batch(cmds...)
	case eventMsg:
		ev := store.Event(msg)
		if ev.Name != "" {
			m.status = fmt.Sprintf("%s %s %s", ev.Name, ev.Kind, ev.Message)
		}
		m.statusErr = ev.Kind == "failed"
		m.refresh()
		return m, waitEvent(m.mgr.Events())
	case reloadMsg:
		var parts []string
		if len(msg.added) > 0 {
			parts = append(parts, "新增 "+strings.Join(msg.added, ","))
		}
		if len(msg.changed) > 0 {
			parts = append(parts, "变更 "+strings.Join(msg.changed, ","))
		}
		if len(msg.removed) > 0 {
			parts = append(parts, "移除 "+strings.Join(msg.removed, ","))
		}
		if len(parts) > 0 {
			m.status = "配置已从磁盘同步: " + strings.Join(parts, " / ")
			m.refresh()
			m.refreshLogs()
		}
		return m, nil
	case opMsg:
		m.applyOpResult(msg)
		m.refresh()
		m.refreshLogs()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch m.mode {
	case modeHelp:
		m.mode = modeList
		return m, nil
	case modeLog:
		switch key {
		case "esc", "q", "l":
			m.mode = modeList
		case "j", "down":
			m.logScroll++
		case "k", "up":
			if m.logScroll > 0 {
				m.logScroll--
			}
		case "g":
			m.logScroll = 0
		case "G":
			m.logScroll = 1 << 30
		}
		return m, nil
	case modeConfirm:
		switch key {
		case "y", "Y", "enter":
			cmd := m.confirm.onYes
			m.confirm = nil
			m.mode = modeList
			return m, cmd
		case "n", "N", "esc", "q":
			m.confirm = nil
			m.mode = modeList
			return m, nil
		}
		return m, nil
	case modeForm:
		return m.handleFormKey(msg)
	}

	if m.filtering {
		switch key {
		case "esc":
			m.filtering = false
			m.filter = ""
		case "enter":
			m.filtering = false
		case "backspace":
			if len(m.filter) > 0 {
				m.filter = m.filter[:len(m.filter)-1]
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.filter += string(msg.Runes)
			}
		}
		m.clampCursor()
		return m, nil
	}

	switch key {
	case "q", "ctrl+c":
		return m.askQuit()
	case "j", "down":
		if m.cursor < len(m.visibleRows())-1 {
			m.cursor++
		}
		m.refreshLogs()
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		m.refreshLogs()
	case "g":
		m.cursor = 0
		m.refreshLogs()
	case "G":
		if rows := m.visibleRows(); len(rows) > 0 {
			m.cursor = len(rows) - 1
		}
		m.refreshLogs()
	case "n":
		m.form = newForm("新建隧道", false, config.Definition{})
		m.mode = modeForm
	case "e":
		if row, ok := m.selected(); ok {
			m.form = newForm("编辑 "+row.Definition.Name, true, row.Definition)
			m.mode = modeForm
		}
	case "s":
		if row, ok := m.selected(); ok {
			name := row.Definition.Name
			m.status = "正在启动 " + name
			return m, startCmd(m.mgr, name)
		}
	case "x":
		if row, ok := m.selected(); ok {
			name := row.Definition.Name
			m.confirm = &confirmState{title: "停止 " + name, detail: "隧道会立即断开，定义保留。", onYes: stopCmd(m.mgr, name)}
			m.mode = modeConfirm
			return m, nil
		}
	case "r":
		if row, ok := m.selected(); ok {
			name := row.Definition.Name
			return m, restartCmd(m.mgr, name)
		}
	case "d":
		if row, ok := m.selected(); ok {
			name := row.Definition.Name
			m.confirm = &confirmState{title: "删除 " + name, detail: "将先停止隧道再删除定义，日志文件保留。", onYes: deleteCmd(m.mgr, name)}
			m.mode = modeConfirm
			return m, nil
		}
	case "l":
		if _, ok := m.selected(); ok {
			m.mode = modeLog
			m.logScroll = 1 << 30
		}
	case "/":
		m.filtering = true
		m.filter = ""
	case "a":
		m.status = "正在启动全部隧道"
		return m, startAllCmd(m.mgr)
	case "A":
		m.confirm = &confirmState{title: "停止全部隧道", detail: "所有隧道都会立即断开。", onYes: stopAllCmd(m.mgr)}
		m.mode = modeConfirm
		return m, nil
	case "?":
		m.mode = modeHelp
	}
	return m, nil
}

func (m Model) handleFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.form == nil {
		m.mode = modeList
		return m, nil
	}
	switch key {
	case "esc":
		m.form = nil
		m.mode = modeList
		return m, nil
	case "tab", "down":
		m.form.focus = (m.form.focus + 1) % len(m.form.fields)
		return m, nil
	case "shift+tab", "up":
		m.form.focus = (m.form.focus - 1 + len(m.form.fields)) % len(m.form.fields)
		return m, nil
	case "ctrl+s":
		return m.saveForm()
	case "enter":
		if m.form.focus == len(m.form.fields)-1 {
			return m.saveForm()
		}
		m.form.focus = (m.form.focus + 1) % len(m.form.fields)
		return m, nil
	}

	f := &m.form.fields[m.form.focus]
	if f.menu != nil {
		if key == " " || key == "enter" || key == "right" {
			f.value = nextMenu(f.menu, f.value)
		} else if key == "left" {
			f.value = prevMenu(f.menu, f.value)
		}
		return m, nil
	}
	switch key {
	case "backspace":
		if len(f.value) > 0 {
			f.value = dropLastRune(f.value)
		}
	case "space":
		f.value += " "
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			f.value += string(msg.Runes)
		}
	}
	return m, nil
}

func (m Model) saveForm() (tea.Model, tea.Cmd) {
	def, err := buildDefinition(m.form)
	if err != nil {
		m.form.err = err.Error()
		return m, nil
	}
	wasRunning := false
	if m.form.editing {
		for _, row := range m.rows {
			if row.Definition.Name == m.form.name {
				wasRunning = supervisor.StateActive(row.Runtime.State)
			}
		}
	}
	m.mode = modeList
	m.form = nil
	return m, saveCmd(m.mgr, def, wasRunning)
}

func (m Model) askQuit() (tea.Model, tea.Cmd) {
	running := 0
	for _, row := range m.rows {
		if supervisor.StateActive(row.Runtime.State) {
			running++
		}
	}
	detail := "当前没有运行中的隧道。"
	if running > 0 {
		detail = fmt.Sprintf("将立即关闭 %d 条运行中的隧道，其生命周期跟随 manager。", running)
	}
	m.confirm = &confirmState{title: "退出 manager", detail: detail, onYes: quitCmd(m.mgr)}
	m.mode = modeConfirm
	return m, nil
}

func (m *Model) applyOpResult(msg opMsg) {
	if msg.err != nil {
		m.status = fmt.Sprintf("%s %s 失败: %v", msg.action, msg.name, msg.err)
		m.statusErr = true
		return
	}
	switch msg.action {
	case "stop":
		m.status = fmt.Sprintf("%s 已停止", msg.name)
	case "delete":
		m.status = fmt.Sprintf("%s 已删除", msg.name)
	case "save":
		m.status = fmt.Sprintf("%s 已保存并生效", msg.name)
	default:
		m.status = fmt.Sprintf("%s %s 完成", msg.name, msg.action)
	}
	m.statusErr = false
}

func (m *Model) refresh() {
	m.rows = m.mgr.Rows()
	m.clampCursor()
}

func (m *Model) clampCursor() {
	rows := m.visibleRows()
	if m.cursor >= len(rows) {
		m.cursor = len(rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) refreshLogs() {
	row, ok := m.selected()
	if !ok {
		m.logLines = nil
		return
	}
	lines, err := logging.Tail(logging.Path(m.logDir, row.Definition.Name), 400)
	if err != nil {
		m.logLines = []string{fmt.Sprintf("读取日志失败: %v", err)}
		return
	}
	m.logLines = lines
}

func (m Model) visibleRows() []supervisor.Row {
	if m.filter == "" {
		return m.rows
	}
	query := strings.ToLower(m.filter)
	out := make([]supervisor.Row, 0, len(m.rows))
	for _, row := range m.rows {
		if strings.Contains(strings.ToLower(row.Definition.Name), query) ||
			strings.Contains(strings.ToLower(row.Definition.Target.Address()), query) {
			out = append(out, row)
		}
	}
	return out
}

func (m Model) selected() (supervisor.Row, bool) {
	rows := m.visibleRows()
	if len(rows) == 0 || m.cursor < 0 || m.cursor >= len(rows) {
		return supervisor.Row{}, false
	}
	return rows[m.cursor], true
}

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	headerStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("246"))
	selectStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	errStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	okStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	frameStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
	fieldStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	focusedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
)

func (m Model) View() string {
	var body string
	switch m.mode {
	case modeHelp:
		body = m.helpView()
	case modeForm:
		body = m.formView()
	case modeConfirm:
		body = m.confirmView()
	case modeLog:
		body = m.logView()
	default:
		body = m.listView()
	}
	return m.headerView() + "\n" + body + "\n" + m.statusView() + "\n" + m.footerView()
}

func (m Model) headerView() string {
	running := 0
	for _, row := range m.rows {
		if supervisor.StateActive(row.Runtime.State) {
			running++
		}
	}
	title := titleStyle.Render("spm " + m.version)
	info := fmt.Sprintf("隧道 %d · 运行中 %d · 目录 %s", len(m.rows), running, m.stateDir)
	if m.filter != "" || m.filtering {
		info += fmt.Sprintf(" · 过滤 %q", m.filter)
	}
	return title + "  " + dimStyle.Render(info)
}

func (m Model) listView() string {
	rows := m.visibleRows()
	if len(rows) == 0 {
		hint := "还没有隧道定义，按 n 新建。"
		if m.filter != "" {
			hint = "没有匹配的隧道。"
		}
		return frameStyle.Width(m.bodyWidth()).Render(dimStyle.Render(hint))
	}
	nameW, targetW, forwardW, stateW, pidW, rstW, uptimeW := m.columnWidths()
	var b strings.Builder
	b.WriteString(dimStyle.Render(fmt.Sprintf("%-*s %-*s %-*s %-*s %-*s %-*s %s",
		nameW, "NAME", targetW, "TARGET", forwardW, "FORWARDS", stateW, "STATE", pidW, "PID", rstW, "RST", "UPTIME")) + "\n")
	for i, row := range rows {
		line := fmt.Sprintf("%-*s %-*s %-*s %-*s %-*s %-*d %s",
			nameW, truncate(row.Definition.Name, nameW),
			targetW, truncate(row.Definition.Target.Address(), targetW),
			forwardW, truncate(row.Definition.ForwardsDisplay(), forwardW),
			stateW, row.Runtime.State,
			pidW, pidText(row.Runtime.PID),
			rstW, row.Runtime.Restarts,
			truncate(uptimeText(row.Runtime), uptimeW),
		)
		line = truncate(line, m.bodyWidth()-2)
		if i == m.cursor {
			line = selectStyle.Render("▸ ") + line
		} else {
			line = "  " + line
		}
		b.WriteString(line + "\n")
	}
	return frameStyle.Width(m.bodyWidth()).Render(strings.TrimRight(b.String(), "\n"))
}

func (m Model) columnWidths() (name, target, forwards, state, pid, rst, uptime int) {
	const separators = 6
	available := m.bodyWidth() - 2 - separators
	state, pid, rst, uptime = 9, 5, 3, 6
	rest := available - (state + pid + rst + uptime)
	if rest < 24 {
		rest = 24
	}
	name = rest * 30 / 100
	target = rest * 30 / 100
	forwards = rest - name - target
	if name < 8 {
		name = 8
	}
	if target < 8 {
		target = 8
	}
	if forwards < 8 {
		forwards = 8
	}
	return
}

func (m Model) logView() string {
	rows := make([]string, 0, logPaneLines)
	lines := m.logLines
	start := 0
	if m.logScroll < 1<<29 {
		start = m.logScroll
	} else if len(lines) > logPaneLines {
		start = len(lines) - logPaneLines
	}
	if start > len(lines) {
		start = len(lines)
	}
	for i := start; i < len(lines) && len(rows) < logPaneLines; i++ {
		rows = append(rows, truncate(lines[i], m.bodyWidth()-4))
	}
	if len(rows) == 0 {
		rows = append(rows, dimStyle.Render("暂无日志"))
	}
	head := headerStyle.Render("日志") + "  " + dimStyle.Render(fmt.Sprintf("第 %d/%d 行", start+1, len(lines)))
	content := head + "\n" + strings.Join(rows, "\n")
	return frameStyle.Width(m.bodyWidth()).Render(content)
}

func (m Model) formView() string {
	if m.form == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render(m.form.title) + "\n\n")
	for i, f := range m.form.fields {
		marker := "  "
		label := fmt.Sprintf("%-10s", f.label)
		value := fieldStyle.Render(f.value)
		if f.value == "" {
			value = dimStyle.Render(f.hint)
		}
		if i == m.form.focus {
			marker = selectStyle.Render("▸ ")
			label = focusedStyle.Render(label)
		}
		b.WriteString(fmt.Sprintf("%s%s %s\n", marker, label, value))
	}
	if m.form.err != "" {
		b.WriteString("\n" + errStyle.Render("✗ "+m.form.err) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("tab/↑↓ 切换字段 · 空格循环选项 · ctrl+s 保存 · esc 取消"))
	return frameStyle.Width(m.bodyWidth()).Render(b.String())
}

func (m Model) confirmView() string {
	if m.confirm == nil {
		return ""
	}
	content := headerStyle.Render(m.confirm.title) + "\n\n" + m.confirm.detail + "\n\n" +
		dimStyle.Render("y 确认 · n/esc 取消")
	return frameStyle.Width(m.bodyWidth()).Render(content)
}

func (m Model) helpView() string {
	lines := []string{
		headerStyle.Render("快捷键"),
		"",
		"n        新建隧道                      e  编辑选中",
		"s        启动                          x  停止",
		"r        重启                          d  删除",
		"l        全屏日志                      /  过滤",
		"a        启动全部                      A  停止全部",
		"j/k ↑/↓  移动                          g/G 首/末",
		"q        退出（会关闭所有隧道）        ?  本页",
		"",
		dimStyle.Render("生命周期绑定：manager 退出时，所有隧道随之退出；"),
		dimStyle.Render("manager 存活期间，隧道异常退出会被自动拉起。"),
	}
	return frameStyle.Width(m.bodyWidth()).Render(strings.Join(lines, "\n"))
}

func (m Model) statusView() string {
	if m.status == "" {
		return dimStyle.Render("就绪")
	}
	if m.statusErr {
		return errStyle.Render("✗ " + m.status)
	}
	return okStyle.Render("✓ " + m.status)
}

func (m Model) footerView() string {
	if m.filtering {
		return dimStyle.Render("过滤: " + m.filter + "▏（enter 确认 · esc 取消）")
	}
	return dimStyle.Render("n 新建 · e 编辑 · s 启动 · x 停止 · r 重启 · d 删除 · l 日志 · a/A 全部 · q 退出 · ? 帮助")
}

func (m Model) bodyWidth() int {
	width := m.width - 4
	if width < 60 {
		width = 60
	}
	return width
}

func newForm(title string, editing bool, def config.Definition) *formState {
	forwards := ""
	ssh := ""
	identity := ""
	restart := config.RestartOnFail
	autostart := "yes"
	if editing {
		forwards = def.ForwardsDisplay()
		ssh = def.Target.Address()
		identity = def.Target.Identity
		restart = def.Restart
		if !def.Autostart {
			autostart = "no"
		}
	}
	return &formState{
		title:   title,
		editing: editing,
		name:    def.Name,
		fields: []field{
			{key: "name", label: "名称", value: def.Name, hint: "例如 anvil-s3-9000"},
			{key: "ssh", label: "SSH", value: ssh, hint: "user@host:port，例如 lyy@hhdev:12880"},
			{key: "identity", label: "私钥", value: identity, hint: "可留空，例如 ~/.ssh/id_rsa"},
			{key: "forwards", label: "转发", value: forwards, hint: "R 127.0.0.1:9000:192.168.179.3:9000，多条用逗号分隔"},
			{key: "restart", label: "重启策略", value: restart, menu: []string{config.RestartOnFail, config.RestartAlways, config.RestartNever}},
			{key: "autostart", label: "自动启动", value: autostart, menu: []string{"yes", "no"}},
		},
	}
}

func buildDefinition(form *formState) (config.Definition, error) {
	if form == nil {
		return config.Definition{}, fmt.Errorf("表单为空")
	}
	get := func(key string) string {
		for _, f := range form.fields {
			if f.key == key {
				return strings.TrimSpace(f.value)
			}
		}
		return ""
	}
	def := config.Definition{
		Name:      get("name"),
		Restart:   get("restart"),
		Backoff:   config.DefaultBackoff(),
		Autostart: get("autostart") != "no",
	}
	if def.Name == "" {
		return def, fmt.Errorf("名称不能为空")
	}
	target, err := config.ParseTarget(get("ssh"))
	if err != nil {
		return def, err
	}
	target.Identity = get("identity")
	def.Target = target
	for _, expr := range strings.Split(get("forwards"), ",") {
		expr = strings.TrimSpace(expr)
		if expr == "" {
			continue
		}
		forward, err := config.ParseForwardExpr(expr)
		if err != nil {
			return def, err
		}
		def.Forwards = append(def.Forwards, forward)
	}
	if len(def.Forwards) == 0 {
		return def, fmt.Errorf("至少需要一条转发规则")
	}
	return def, nil
}

func nextMenu(menu []string, current string) string {
	for i, item := range menu {
		if item == current {
			return menu[(i+1)%len(menu)]
		}
	}
	return menu[0]
}

func prevMenu(menu []string, current string) string {
	for i, item := range menu {
		if item == current {
			return menu[(i-1+len(menu))%len(menu)]
		}
	}
	return menu[0]
}

func dropLastRune(s string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return s
	}
	return string(runes[:len(runes)-1])
}

func pidText(pid int) string {
	if pid <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d", pid)
}

func uptimeText(rt store.Runtime) string {
	if rt.StartedAt.IsZero() || !supervisor.StateActive(rt.State) {
		return "-"
	}
	return time.Since(rt.StartedAt).Truncate(time.Second).String()
}

func truncate(s string, limit int) string {
	runes := []rune(s)
	if limit <= 0 || len(runes) <= limit {
		return s
	}
	if limit < 2 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func waitEvent(ch <-chan store.Event) tea.Cmd {
	return func() tea.Msg {
		return eventMsg(<-ch)
	}
}

func startCmd(mgr *supervisor.Manager, name string) tea.Cmd {
	return func() tea.Msg {
		return opMsg{name: name, action: "start", err: mgr.Start(name)}
	}
}

func stopCmd(mgr *supervisor.Manager, name string) tea.Cmd {
	return func() tea.Msg {
		return opMsg{name: name, action: "stop", err: mgr.Stop(name)}
	}
}

func restartCmd(mgr *supervisor.Manager, name string) tea.Cmd {
	return func() tea.Msg {
		return opMsg{name: name, action: "restart", err: mgr.Restart(name)}
	}
}

func deleteCmd(mgr *supervisor.Manager, name string) tea.Cmd {
	return func() tea.Msg {
		return opMsg{name: name, action: "delete", err: mgr.DeleteDefinition(name)}
	}
}

func saveCmd(mgr *supervisor.Manager, def config.Definition, wasRunning bool) tea.Cmd {
	return func() tea.Msg {
		_, exists := mgr.Definition(def.Name)
		var err error
		if exists {
			err = mgr.UpdateDefinition(def)
		} else {
			err = mgr.AddDefinition(def)
		}
		if err == nil && wasRunning {
			err = mgr.Restart(def.Name)
		}
		return opMsg{name: def.Name, action: "save", err: err}
	}
}

func startAllCmd(mgr *supervisor.Manager) tea.Cmd {
	return func() tea.Msg {
		errs := mgr.StartAll()
		if len(errs) > 0 {
			return opMsg{name: "全部", action: "start", err: errs[0]}
		}
		return opMsg{name: "全部", action: "start"}
	}
}

func stopAllCmd(mgr *supervisor.Manager) tea.Cmd {
	return func() tea.Msg {
		errs := mgr.StopAll()
		if len(errs) > 0 {
			return opMsg{name: "全部", action: "stop", err: errs[0]}
		}
		return opMsg{name: "全部", action: "stop"}
	}
}

func quitCmd(mgr *supervisor.Manager) tea.Cmd {
	return func() tea.Msg {
		mgr.Shutdown()
		return tea.Quit()
	}
}

func reloadCmd(mgr *supervisor.Manager) tea.Cmd {
	return func() tea.Msg {
		added, removed, changed := mgr.ReloadDefinitions()
		return reloadMsg{added: added, removed: removed, changed: changed}
	}
}
