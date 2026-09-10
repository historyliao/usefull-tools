# ssh-proxy-manager (spm)

管理 `ssh -N -R 127.0.0.1:9223:127.0.0.1:9222 lyy@hhdev` 这类隧道：**原生 macOS 界面 App + Go 内核**。
代理分**正向代理**（本地端口 → 远端服务）和**反向代理**（远端端口 → 本机服务）两类。

打开界面就开工作台，退出界面就把所有隧道一起收走——界面进程崩了也一样（内核靠 stdin 断裂感知）。

设计取舍与不变量见 [DESIGN.md](DESIGN.md)。

## 安装

界面 App（推荐，内核已打包在里面，不需要另外装 `spm`）：

```bash
cd ssh-proxy-manager
make build          # 产出 build/SSH Proxy Manager.app
make run            # 直接打开
open "build/SSH Proxy Manager.app"
```

只想用命令行：

```bash
cd ssh-proxy-manager
go build -o spm ./cmd/spm
sudo install -m 0755 spm /usr/local/bin/spm && rm spm   # 可选：装成全局命令
```

不想安装就直接 `go run ./cmd/spm`。

## 快速开始

### 图形界面

```bash
make run
```

右上角「新建代理」→ 选类型（正向/反向）→ 填名称、SSH 目标、端口映射 → 创建。列表按方向分成两个分区，
每行右侧的按钮直接启停，选中后右侧是详情与日志。常用操作：全部启动/全部停止、编辑、删除、菜单栏图标里快速启停、
`⌘N` 新建。

### 命令行

```bash
# 1) 只写定义，此时不会启动任何东西
spm create --name chrome-9223 --ssh hhdev -R 127.0.0.1:9223:127.0.0.1:9222

# 2) 打开界面；autostart 的条目会自动被拉起
spm ui

# 或者不用界面，直接在终端里托管
spm run
```

`--ssh` 支持三种写法，都会原样交给 ssh 处理，所以 `~/.ssh/config` 里的别名、User、Port、ProxyJump 都照常生效：

```text
hhdev              # ssh 配置里的别名，最省事
lyy@hhdev          # 只写用户
lyy@117.50.113.135:12880
```

## 正向代理与反向代理

| 方向 | 一句话 | 转发写法 | 典型例子 |
| --- | --- | --- | --- |
| 反向代理 | 远端端口 → 本机服务 | `-R 远端监听地址:远端端口:本机目标地址:目标端口` | `-R 127.0.0.1:9223:127.0.0.1:9222` |
| 正向代理 | 本机端口 → 远端服务 | `-L 本机监听地址:本机端口:远端目标地址:目标端口` | `-L 127.0.0.1:8080:192.168.179.3:8080` |
| 正向代理 | 本机 SOCKS 代理 | `-D 本机监听地址:本机端口` | `-D 127.0.0.1:1080` |

同一条定义只能属于一个方向，不能把 `-R` 和 `-L/-D` 混在一起（创建时会直接报错，请拆成两条）。

### 反向代理（-R）字段逐项说明

链路是：**访问者 → 远端服务器的 `远端监听地址:远端端口` →（ssh 隧道）→ 你这台 Mac 的 `本机目标地址:目标端口`**。
也就是说，端口开在远端，真正的服务在本机，两边分别填"远端听哪儿"和"本机连哪儿"。

| 字段 | 含义 | 注意 |
| --- | --- | --- |
| 远端监听地址 `bind_host` | 在**远端服务器**上监听哪个地址，默认 `127.0.0.1` | 只写 `127.0.0.1` 时只有远端机器自己能连；要让别的机器（含远端其他网卡）连，得写 `0.0.0.0` 或远端网卡 IP，并且远端 sshd 必须允许（`GatewayPorts yes` / `clientspecified`），否则会被强制回落到 loopback |
| 远端监听端口 `bind_port` | 访问者在**远端**连的端口 | 已被占用时 `ExitOnForwardFailure=yes` 会让 ssh 立刻退出，内核退避重试，连续 5 次失败转 `failed` |
| 本机目标地址 `dest_host` | **从你这台 Mac** 发起连接的目标 | `127.0.0.1` = 这台 Mac 自己；也可以写本机能访问到的内网地址 |
| 本机目标端口 `dest_port` | 本机目标服务的端口 | 例如 Chrome 的调试端口 `9222` |

具体例子——把你 Mac 上的 Chrome 调试端口反向暴露到 `hhdev` 的 9223：

```bash
# 本机先起 Chrome
/Applications/Google\ Chrome.app/Contents/MacOS/Google\ Chrome --remote-debugging-port=9222

# 远端 9223 → 本机 9222
spm create --name chrome-9223 --ssh hhdev -R 127.0.0.1:9223:127.0.0.1:9222
```

画成链路图：

```text
[ 远端 hhdev 上的访问者 ]
        │ 连 127.0.0.1:9223
        ▼
[ hhdev: sshd 监听 127.0.0.1:9223 ]  ←── 这一端由 bind_host:bind_port 决定
        │ ═══ ssh 隧道（-R）═══
        ▼
[ 你的 Mac 上由 ssh 客户端发起连接 ]  ←── 这一端由 dest_host:dest_port 决定
        │ 连 127.0.0.1:9222
        ▼
[ 本机 Chrome 调试端口 9222 ]
```

自检：

```bash
ssh hhdev 'ss -ltn | grep 9223'          # 远端确实在监听
ssh hhdev 'curl -s 127.0.0.1:9223/json/version | head -3'   # 从远端经隧道摸到本机 Chrome
```

常见坑：

- **端口占用**：日志里会出现 `remote port forwarding failed for listen port 9223`，然后退避重启，5 次后 `failed`——先确认远端那个端口没被旧的 ssh/tmux 隧道占着。
- **别的机器连不上**：因为只监听了 `127.0.0.1`。远端改 `0.0.0.0` + `GatewayPorts` 才能对外开放，且注意这意味着把你本机的服务暴露出去。
- **目标地址写错网络**：`dest_host` 是从本机侧连的，写 `192.168.179.3` 指的是"这台 Mac 能路由到 192.168.179.3"，跟远端内网无关。
- 一条定义可以挂多条 `-R`（同一个 ssh 连接里开多个远端端口），在界面里点「添加一条」即可。

### 反向代理没有 `-D`（远端 SOCKS）

ssh 的三个转发选项里，`-D`（SOCKS 动态转发）**只有本地版本**，没有"远端 SOCKS"这个能力：

| 选项 | 监听在哪 | 目标怎么定 |
| --- | --- | --- |
| `-L` | 本机 | 建定义时写死一个 `目标:端口` |
| `-D` | 本机 | 运行时由 SOCKS 协议决定，任意目标 |
| `-R` | 远端 | 建定义时写死一个 `本机目标:端口` |

所以反向代理只能一条条配固定目标（可以配多条 `-R`），不存在 `-R` 版的 SOCKS。

确实需要"远端程序用 SOCKS、出口在你这台 Mac"时，用两条定义组合出来——把本机的 SOCKS 端口反向暴露给远端：

```bash
# ① 正向：在本机开一个 SOCKS 端口（这个例子里它的出口是 hhdev）
spm create --name mac-socks --ssh hhdev -D 127.0.0.1:18080

# ② 反向：把本机这个 SOCKS 端口暴露到远端 18081
spm create --name expose-socks --ssh hhdev -R 127.0.0.1:18081:127.0.0.1:18080
```

之后远端任何支持 SOCKS 的程序（浏览器、`curl --socks5-hostname`、绝大多数 CLI）把 `127.0.0.1:18081` 当 SOCKS 用即可。
实测：在远端起 `curl -s --socks5-hostname 127.0.0.1:18081 https://example.com` 能拿到页面——这条 SOCKS 实际跑在你 Mac 上，
每个连接的出口由那条 `-D` 定义决定（上面例子里是 hhdev；换成指向别处的 SOCKS 服务，出口就在那一边）。

### 正向代理：`-L` 与 `-D` 的区别

| | `-L` 本地转发 | `-D` 动态转发（SOCKS） |
| --- | --- | --- |
| 本机监听的是什么 | 一个普通 TCP 端口，固定指向一个目标 | 一个 SOCKS4/SOCKS5 代理端口，目标不固定 |
| 目标什么时候确定 | 建定义时写死（`目标:端口`） | 运行时由客户端通过 SOCKS 协议告诉它 |
| 连接从哪发起 | 远端 sshd 发起 | 远端 sshd 发起（同一个出口） |
| 覆盖范围 | 一个目标；要多个目标就加多条 `-L` | 任意目标，一个端口全包 |
| 对客户端的要求 | 无，任何 TCP 客户端都能连 | 客户端必须支持 SOCKS（浏览器、`curl --socks5`、多数 CLI） |
| 域名在哪解析 | 远端（目标名交给远端 sshd 解析，所以能直达远端内网域名） | 看客户端：`socks5h`（远端解析）还是 `socks5`（本地解析后发 IP） |
| 典型用法 | 访问某个固定的远端内网服务（数据库、内部 web） | 让浏览器/整机流量走远端网络、换出口 |
| 监听地址写 `0.0.0.0` | 同网段其他机器能用你本机这个端口 | 等于把代理开放给同网段，谨慎 |

一句话：**`-L` 是"这个本地端口永远通向那一个远端目标"，`-D` 是"这个本地端口是个代理，通向哪儿由连上来的程序说"**。

## 命令

| 命令 | 说明 |
| --- | --- |
| `spm ui` | 打开界面 App |
| `spm run [--watch-stdin]` | 无界面内核（后台托管隧道）；`--watch-stdin` 供界面托管用 |
| `spm`（或 `spm tui`） | 终端界面（备用） |
| `spm start\|stop\|restart <name>` | 让运行中的内核启停某条代理 |
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

1. **生命周期绑定**：退出 App、`q` 退出终端界面、Ctrl-C、关掉终端窗口、`kill -9`、进程崩溃——任何一条路径下，内核管理的隧道都会在数秒内被终止，远端监听端口随之释放。这是设计语义，不是缺陷。
2. **关闭窗口 ≠ 退出**：关窗口隧道继续跑（App 留在菜单栏）；点「退出」或 `⌘Q` 才收走全部隧道。
3. **App 与内核的关系**：App 发现没有内核在跑就自己拉起一个（退出 App 一起收走）；内核已经在跑时（比如你先在终端 `spm run`）App 直接附加过去，此时退出 App 不影响隧道。
4. **同时只能有一个内核**：`~/.ssh-proxy-manager/lock` 是独占锁，App 与 `spm run` / `spm`（终端界面）互斥。`create` / `list` / `delete` / `logs` 直接读写文件不占锁；`start` / `stop` / `restart` 走控制通道请内核代劳，也不占锁。
5. **隧道进程的 stdin 是 `/dev/null`**：密码与私钥口令必须走 ssh-agent，host key 必须已在 known_hosts，否则会启动失败并进入退避重启。内核自身不碰任何凭据。
6. **只纳管自己拉起的隧道**：在 tmux 里手工起的 `ssh -N` 不会被接管，也不会被清理。
7. **`autostart` 的时机是内核启动时**，不是 `create` 时。在界面里新建条目后需要点一下启动（或重启 App）。

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
  ├── control.sock     控制通道，界面/CLI 与运行中的内核通信
  ├── kernel.log        界面托管内核时的输出与退出原因
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
tail -f ~/.ssh-proxy-manager/kernel.log      # 内核自己的输出（界面托管时）
ssh hhdev 'ss -ltn | grep 9223'   # 确认远端端口是否真的在监听 / 是否已释放
```

常见现象：

- 界面顶部显示「内核未运行」：点旁边的「启动内核」；如果仍失败，看 `~/.ssh-proxy-manager/kernel.log`（常见原因是锁被另一个 `spm run`/TUI 占着，或内核二进制不在）。
- 「找不到 spm 可执行文件」：App 正常构建时内核打包在 `Contents/MacOS/spm`，若你单独运行 `spm ui` 打开的是别处的 App，用 `SPM_BIN` 指定内核路径。
- `failed`：连续失败超过 5 次。多半是远端端口被占、host key 变更、或无法非交互认证；`spm logs` 能看到 ssh 的原始报错。
- 启动即退避重启：`ExitOnForwardFailure=yes` 下远端端口被占用会立刻退出，日志里会有 `remote port forwarding failed for listen port ...`。
- `create` 报端口冲突：`-R` 在同一个 target 上占用相同的远端 `bind_host:bind_port`，或 `-L`/`-D` 占用相同的本地端口，创建阶段就会拒绝。
- 报「正向代理不支持 -R 转发」：同一条定义混了方向，拆成一条正向 + 一条反向。
- 远端 `-R` 的 bind_host 默认写死 `127.0.0.1`；要听 `0.0.0.0` 需要显式配置，且远端 sshd 要开 `GatewayPorts`。
