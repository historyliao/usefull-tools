# ssh-proxy-manager 设计文档

## 一、目标与定位

管理形如 `ssh -N -R 127.0.0.1:9000:192.168.179.3:9000 lyy@hhdev -p 12880` 的隧道：可以新建、关闭、删除，
并且用一条明确的规则把隧道生命周期绑到管理进程上。

一句话定义：**一个 Go 单二进制，带 TUI 的前台管理器；TUI 进程就是 manager 本身。**

它不是守护进程，也不做"后台常驻 + 客户端 attach"那一套：打开它就开工作台，退出它就把所有隧道一起收走。

## 二、不变量

| 编号 | 不变量 | 说明 |
| --- | --- | --- |
| INV-1 | manager 存活期间，running 条目的 ssh 进程必须存在 | supervisor 检测退出并退避重启 |
| INV-2 | manager 以任何方式退出，都必须终止它管理的全部代理，不留孤儿 | 含正常退出、Ctrl-C、SIGHUP、`kill -9`、进程崩溃 |
| 例外 | 代理自身中断（远端断开、端口被占、鉴权失败） | 按重启策略处理，不算违反上述两条 |

INV-2 的直接后果需要提前接受：**误关终端窗口等于隧道全断**。这是"生命周期绑定"语义的必然结果，不是实现缺陷。

## 三、进程模型

```text
┌──────────────────────────────────────────────┐
│ spm (TUI 前台进程 = manager)                  │
│  ├─ supervisor: 检测退出 → 退避 → 重启         │
│  ├─ TUI (bubbletea): 列表/表单/日志/确认框     │
│  ├─ 持有每个代理的 death-pipe 写端             │
│  └─ 状态: config.json / state.json / events    │
└───────────────┬──────────────────────────────┘
                │ os.Pipe() 写端留在这边（唯一持有者）
                │ ExtraFiles 把读端作为 fd 3 传给 guard
                ▼
        ┌───────────────────────┐
        │ spm __guard --name X  │  ← 极薄进程，只做两件事
        └───────────┬───────────┘
                    │ Setpgid=true，独立进程组
                    ▼
     ssh -N -R 127.0.0.1:9000:192.168.179.3:9000 lyy@hhdev -p 12880
     (-L/-D 同理；stdin=/dev/null，stdout/stderr → logs/<name>.log)
```

### guard 的职责

1. 启动 ssh 子进程（独立进程组），把 ssh pid 通过状态管道回报给 manager；
2. 监听 death pipe，读到 EOF 就对 ssh 进程组执行 `SIGTERM` → 宽限 5s → `SIGKILL`；
3. 收到 `SIGTERM`/`SIGINT`/`SIGHUP` 时执行同样的清理；
4. ssh 自行退出时，用同样的退出码退出，让 manager 感知。

### 三个必须钉死的实现细节

1. **death pipe 写端绝不能被 guard/ssh 继承**。子进程里必须只拿到读端，写端留在 manager 且带 `O_CLOEXEC`；
   否则写端始终有人持有，EOF 永远不来，`kill -9` manager 后代理就变孤儿，INV-2 直接失效。
2. **日志 fd 与 death pipe 完全分离**。日志文件描述符谁继承都行，但它不参与 EOF 判定。
3. **杀进程用进程组**（`kill(-pgid, SIGTERM)`），先 `SIGTERM` 是因为 ssh 会自己拆远端转发，比强杀干净。

## 四、退出路径矩阵

| manager 退出方式 | 覆盖机制 |
| --- | --- |
| TUI 里 `q` 退出 | 二次确认（列出将关闭的隧道数）→ 显式 shutdown |
| Ctrl-C / SIGTERM / SIGHUP / SIGQUIT | `signal.Notify` 捕获 → 同一条 shutdown 路径 |
| `kill -9` / panic / OOM | guard 的 death pipe EOF 兜底 |
| 终端窗口关闭 | SIGHUP + death pipe 双保险 |
| 机器重启 | 启动时 reconcile：按 `pid + 进程启动时间 + cmdline` 三元组校验，清理上一轮残留 |

shutdown 固定顺序：置 epoch（让所有在途 restart 立刻失效）→ 逐代理 `SIGTERM` → 宽限 5s → `SIGKILL`
→ 写 state → 释放锁 → 退出。

## 五、命令行与 TUI 的分工

生命周期绑定决定了一件事：**凡是启动隧道的进程，必须一直活着**。因此短命的 CLI 子命令不能启动隧道
（它一退出 guard 就会收掉 ssh），CLI 只做定义与只读查询：

| 入口 | 职责 |
| --- | --- |
| `spm` | 进 TUI，即 manager：建/启/停/重启/删除隧道，看日志 |
| `spm run` | 无界面 supervisor，同一套核心，便于脚本化与排障 |
| `spm create` | 只写定义，不启动 |
| `spm list [--json]` | 定义 + 运行态只读展示 |
| `spm delete <name>` | 删除定义（运行中的条目由 manager 侧收敛） |
| `spm logs <name> [-f]` | 读日志文件 |
| `spm __guard` | 隐藏模式，由 supervisor 内部调用 |

## 六、数据与目录

```text
~/.ssh-proxy-manager/
  ├── config.json      定义（0600，可手工编辑）
  ├── state.json       运行态（manager 独占写，临时文件 + rename 原子替换）
  ├── events.jsonl     事件流（start/stop/exit/restart/failed）
  ├── logs/<name>.log  每代理一份
  └── lock             单实例锁（flock）
```

条目示例：

```json
{
  "name": "anvil-s3-9000",
  "target": { "user": "lyy", "host": "hhdev", "port": 12880, "identity": "~/.ssh/id_rsa" },
  "forwards": [
    { "type": "R", "bind_host": "127.0.0.1", "bind_port": 9000,
      "dest_host": "192.168.179.3", "dest_port": 9000 }
  ],
  "restart": "on-failure",
  "backoff": { "initial": 1, "max": 60, "max_attempts": 5 },
  "autostart": true
}
```

运行态字段：`state / pid / guard_pid / proc_start_time / started_at / restarts / last_exit_code / last_exit_at / last_error`。
`proc_start_time` 不能省：只凭 pid 判活会被 PID 复用骗到，可能误杀无关进程。

状态机：`stopped → starting → running → stopping → stopped`，异常分支 `restarting(backoff)` 与 `failed`。
只有 supervisor 改运行态，TUI 只发命令。

## 七、ssh 命令生成

固定注入：`-N -o ExitOnForwardFailure=yes -o ServerAliveInterval=15 -o ServerAliveCountMax=3 -o TCPKeepAlive=yes`。

- `-R` 的 bind_host 默认写死 `127.0.0.1`；要 `0.0.0.0` 必须显式配置，且需要远端 `GatewayPorts` 支持；
- 不自动加 `StrictHostKeyChecking=no`，保持主机密钥校验；
- 需要 passphrase 走 ssh-agent，manager 不接触凭据；
- 支持 `-R` / `-L` / `-D`，单条目可挂多条转发。

## 八、TUI 交互

主界面：上方为隧道列表（name、target、转发、state、pid、uptime、restarts、last error），
下方为选中条目的日志尾部，底部为状态栏与快捷键提示。

进入 TUI 之前先 reconcile 上一轮残留，再按 `autostart` 拉起条目，与 `spm run` 的行为一致。

| 按键 | 动作 |
| --- | --- |
| `↑/↓` `j/k` | 选择条目 |
| `n` | 新建（表单：name、ssh、identity、forwards、restart、autostart） |
| `e` | 编辑选中条目（运行中改动需重启生效） |
| `s` / `x` | 启动 / 停止 |
| `r` | 重启 |
| `d` | 删除（二次确认，先停止再删定义，日志保留） |
| `l` | 全屏日志 |
| `/` | 过滤（name/target） |
| `a` / `A` | 全部启动 / 全部停止 |
| `q` | 退出：弹确认，写明"将关闭 N 条隧道" |

工程约定：启停、探活等操作必须异步投递为 `tea.Msg`，渲染线程内禁止阻塞调用，
否则 ssh 握手慢时整个界面会卡死。

## 九、代码结构

```text
ssh-proxy-manager/
  ├── cmd/spm/main.go            # 入口：无参进 TUI，有子命令走 CLI
  ├── internal/config/           # 定义读写、校验、原子落盘
  ├── internal/store/            # state/events、单实例锁
  ├── internal/supervisor/       # spawn、wait、退避、epoch、reconcile
  ├── internal/guard/            # __guard 模式：death pipe → 杀进程组
  ├── internal/sshcmd/           # 命令与参数生成（-R/-L/-D）
  ├── internal/tui/              # bubbletea 模型/视图/表单
  └── internal/logging/          # 每代理日志
```

TUI 用 bubbletea + lipgloss；进程与系统调用只用标准库（`os/exec`、`syscall`、`os/signal`）。

## 十、重启策略

- 退出码 0 且策略为 `on-failure` → 视为正常结束，不重启；
- 非 0 退出 / 被信号杀死 → 退避重启：`min(initial * 2^(n-1), max)` 秒；
- 连续失败超过 `max_attempts` → 置 `failed` 并停止重试，避免远端端口被占时打出重启风暴；
- 稳定运行超过 30s 后重置失败计数；
- 用户主动 stop 或 shutdown 期间的退出 → 不重启（靠 epoch/generation 标记区分，避免停止与重启竞态）。

## 十一、验收用例

1. manager 存活时 `kill -9` 某代理 → 退避窗口内被拉起，pid 变化、`restarts+1`（INV-1）。
2. `q` 退出、Ctrl-C、`kill -9` manager、整窗关闭 → 四条路径下所有代理都在数秒内消失，`ps` 无残留（INV-2）。
3. 退出后远端端口确实释放（远端 `ss -ltn` 无对应监听），证明不是"本地看着干净、远端还挂着"。
4. 退避重启中退出 → restart 被 epoch 打断，不会反向拉起。
5. 启动时若发现上轮残留 → 按三元组校验后清理，且不会误杀同名的无关进程。
6. 同名 `create` 报错且不产生第二个进程；`stop` 之后不被自动拉起。
7. guard 的 EOF 行为必须有回归测试：起真实代理，`kill -9` 父进程，断言 ssh 进程组秒级消失。

## 十二、已知坑

- 远端 `-R` 端口残留依赖连接断开才释放，正常退出务必先 `SIGTERM` 而不是直接 `SIGKILL`；
- 同一远端 `bind_port` 冲突要在 `create` 时预警；
- `ExitOnForwardFailure=yes` 遇到端口被占会立刻退出，必须靠退避与失败上限兜住；
- `-D` 是本地 SOCKS 端口，字段语义与 `-L`/`-R` 不同，UI 上要区分；
- guard 若被 `SIGKILL`，ssh 会成为孤儿，manager 停止时需用记录的 ssh pid 兜底清理；
- 只认自己拉起的隧道，不纳管已有进程（例如 tmux 里手工起的隧道）。
