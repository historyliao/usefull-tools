# ssh-proxy-manager (spm)

管理 `ssh -N -R 127.0.0.1:9223:127.0.0.1:9222 lyy@hhdev` 这类隧道的单二进制工具：
**带 TUI 的前台管理器，TUI 进程本身就是 manager**。打开它就开工作台，退出它就把所有隧道一起收走。

设计取舍与不变量见 [DESIGN.md](DESIGN.md)。

## 安装

```bash
cd ssh-proxy-manager
go build -o spm ./cmd/spm
sudo install -m 0755 spm /usr/local/bin/spm && rm spm   # 可选：装成全局命令
```

不想安装就直接 `go run ./cmd/spm`。

## 快速开始

```bash
# 1) 只写定义，此时不会启动任何东西
spm create --name chrome-9223 --ssh hhdev -R 127.0.0.1:9223:127.0.0.1:9222

# 2) 打开工作台：autostart 的条目会自动被拉起
spm
```

`--ssh` 支持三种写法，都会原样交给 ssh 处理，所以 `~/.ssh/config` 里的别名、User、Port、ProxyJump 都照常生效：

```text
hhdev              # ssh 配置里的别名，最省事
lyy@hhdev          # 只写用户
lyy@117.50.113.135:12880
```

## 命令

| 命令 | 说明 |
| --- | --- |
| `spm`（或 `spm tui`） | 打开 TUI，即 manager 本体 |
| `spm run` | 无界面 supervisor，行为和 TUI 一致，便于脚本化与排障 |
| `spm create ...` | 只写定义，不启动（别名 `add`） |
| `spm list [--json]` | 定义 + 运行态只读展示（别名 `ls`） |
| `spm delete <name>` | 删除定义（别名 `rm`/`remove`） |
| `spm logs <name> [-f] [-n 200]` | 读日志，`-f` 持续输出 |
| `spm version` / `spm help` | 版本 / 帮助 |

通用参数 `--dir <path>`，环境变量 `SPM_DIR` 指定状态目录；`SPM_SSH_BIN` 可替换 ssh 可执行文件（测试用）。

### create 常用参数

```bash
spm create --name anvil-s3 \
  --ssh lyy@hhdev:12880 \
  --identity ~/.ssh/id_rsa \
  -R 127.0.0.1:9000:192.168.179.3:9000 \
  -L 127.0.0.1:5176:127.0.0.1:5176 \
  -D 127.0.0.1:1080 \
  --restart on-failure \
  --autostart=true
```

- `-R` / `-L` 形如 `bind_host:bind_port:dest_host:dest_port`，`-D` 形如 `bind_host:bind_port`；都可重复。
  也可以用 `--forward "R 127.0.0.1:9000:192.168.179.3:9000"` 一次给一条（不写类型时默认 `R`）。
- `--identity` 可留空，交给 `~/.ssh/config` 或 agent 决定。
- `--restart` 取 `always` / `on-failure` / `never`，默认 `on-failure`。
- `--autostart` 默认 `true`：manager 启动时自动拉起该条目。
- `--extra-arg` 透传额外 ssh 参数，一次一个参数，可重复。

## TUI 快捷键

| 按键 | 动作 |
| --- | --- |
| `↑/↓` `j/k`、`g/G` | 选择条目、跳到首/末 |
| `n` / `e` | 新建 / 编辑选中条目（运行中改动需重启生效） |
| `s` / `x` / `r` | 启动 / 停止 / 重启 |
| `d` | 删除（二次确认，先停隧道再删定义，日志保留） |
| `l` | 全屏日志（`j/k` 滚动，`g/G` 首/末，`esc` 返回） |
| `/` | 按名称或 target 过滤 |
| `a` / `A` | 全部启动 / 全部停止 |
| `?` | 帮助 |
| `q` | 退出：二次确认，写明将关闭几条隧道 |

主界面下方是选中条目的日志尾部，底部状态栏显示最近一次事件（start / stop / exit / restart / failed）。

## 必须知道的几条语义

1. **生命周期绑定**：`q` 退出、Ctrl-C、SIGHUP、关掉终端窗口、`kill -9`、进程崩溃——任何一条路径下，manager 管理的隧道都会在数秒内被终止，远端监听端口随之释放。误关终端窗口等于隧道全断，这是设计语义，不是缺陷。
2. **同时只能有一个 manager**：`spm` 与 `spm run` 互斥（`~/.ssh-proxy-manager/lock` 独占锁）。`create` / `list` / `delete` / `logs` 不占锁，可以在 manager 运行期间从另一个终端使用，改动会在 5 秒内被 manager 同步：外部新增的条目不会自动启动，外部删除的条目会被 manager 停掉并移除。
3. **隧道进程的 stdin 是 `/dev/null`**：密码与私钥口令必须走 ssh-agent，host key 必须已在 known_hosts，否则会启动失败并进入退避重启。manager 自身不碰任何凭据。
4. **只纳管自己拉起的隧道**：在 tmux 里手工起的 `ssh -N` 不会被接管，也不会被清理。
5. **`autostart` 的时机是 manager 启动时**，不是 `create` 时。在 TUI 里新建条目后需要按 `s` 启动。

## 重启与失败处理

- `on-failure`：退出码为 0 视为正常结束不重启；非 0 退出、被信号杀死、启动失败则退避重启。
- 退避：`min(1 × 2^(n-1), 60)` 秒；连续失败超过 5 次置 `failed` 并停止重试，等人工介入，避免远端端口被占时打出重启风暴。
- 稳定运行满 30 秒后失败计数归零。
- 主动 `x` 停止、或 manager 退出期间的退出不会触发重启。

## 数据目录

```text
~/.ssh-proxy-manager/          # 可用 --dir / SPM_DIR 覆盖
  ├── config.json      定义（0600，可手工编辑，manager 5 秒内同步）
  ├── state.json       运行态：state/pid/proc_start_time/proc_cmdline/restarts/last_error…
  ├── events.jsonl     事件流：start/stop/exit/restart/failed
  ├── logs/<name>.log  每个隧道一份，首行是完整的 ssh 命令行
  └── lock             单实例锁
```

状态机为 `stopped → starting → running → stopping → stopped`，异常分支 `restarting(backoff)` 与 `failed`，
只有 supervisor 写运行态，TUI 只发命令。

## ssh 参数

固定注入 `-N -o ExitOnForwardFailure=yes -o ServerAliveInterval=15 -o ServerAliveCountMax=3 -o TCPKeepAlive=yes`；
非 22 端口追加 `-p`，配置了 `--identity` 时追加 `-i`。不注入 `StrictHostKeyChecking=no`，主机密钥校验保持开启。

## 排障

```bash
spm list                       # 定义 + 状态 + 最近错误，先看这里
spm logs <name> -f             # 跟随某个隧道的 ssh 输出
tail -f ~/.ssh-proxy-manager/events.jsonl
ssh hhdev 'ss -ltn | grep 9223'   # 确认远端端口是否真的在监听 / 是否已释放
```

常见现象：

- `failed`：连续失败超过 5 次。多半是远端端口被占、host key 变更、或无法非交互认证；`spm logs` 能看到 ssh 的原始报错。
- 启动即退避重启：`ExitOnForwardFailure=yes` 下远端端口被占用会立刻退出，日志里会有 `remote port forwarding failed for listen port ...`。
- `create` 报端口冲突：`-R` 在同一个 target 上占用相同的远端 `bind_host:bind_port`，或 `-L`/`-D` 占用相同的本地端口，创建阶段就会拒绝。
- 远端 `-R` 的 bind_host 默认写死 `127.0.0.1`；要听 `0.0.0.0` 需要显式配置，且远端 sshd 要开 `GatewayPorts`。
