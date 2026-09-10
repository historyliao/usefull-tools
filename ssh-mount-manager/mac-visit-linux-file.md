# Mac 访问 Linux 服务器文档：SSHFS 挂载执行路径

## 一、总体方案

核心需求可以用一句话概括：

> **Linux 是文档产生端，Mac 是文档阅读端。**

设计上遵循一条明确的分层原则：

```text
传输层（SSHFS）：只负责把文件字节原封不动搬过来
                 不转换内容、不改文件名、不生成额外文件

渲染层（浏览器 / 查看器）：只负责“怎么显示”，不碰文件本身
```

也就是说，**挂载点里永远是原始文件**（`.md` 还是 `.md`，`.html` 还是 `.html`），Markdown → HTML 这类转换要么发生在源端（Linux），要么发生在读取端（浏览器插件），**绝不出现在传输链路上**。

```text
        Linux Server
        ┌──────────────────────────┐
        │ ~/docs                  │  ← 源端：只生成原始文件
        │  ├── *.html              │
        │  ├── *.md                │
        │  └── css / js / images   │
        └───────────┬──────────────┘
                    │ SSH / SFTP（字节透明）
                    ▼
        macOS    macFUSE + SSHFS
                    │  只搬运，不改内容
                    ▼
              ~/mnt/remote-docs          ← 与远端逐字节一致的本地视图
                    │
        ┌───────────┴─────────────┐
        ▼                          ▼
   .html 浏览器原生渲染       .md 由“读取端渲染器”显示
                             （浏览器 Markdown 插件 / 查看器）
```

关键角色：

- **macFUSE**：macOS 上的 FUSE 框架，只提供“让用户态程序能挂载文件系统”的能力，本身不实现 SSHFS。
- **SSHFS**：真正干活的那一层，通过 SFTP 协议把远端目录映射为本地挂载点，需要单独安装。它是**字节透明的传输通道**，不参与任何内容转换。
- **~/mnt/remote-docs**：Mac 上的挂载点，之后所有“本地文件”操作都指向它。
- **浏览器 Markdown 插件**：读取端的渲染器，负责把 `.md` 显示成网页，文件本身和传输链路都不参与转换。

为什么第一版选 SSHFS，而不是 rclone mount：

```text
SSH server + SFTP + sshfs
```

这条链路最短、最直接，出问题时排查路径清晰。rclone mount 也能通过 FUSE + SFTP backend 挂载，但多了一层配置，适合后续有 rclone 特定需求时再引入。

一个必须提前说明的差异：**HTML 和 Markdown 的处理方式完全不同**。

```text
.html → 浏览器原生就能渲染                     ✔ file:// 直接打开
.md   → 浏览器本身不认，需要“读取端渲染器”     → 由浏览器插件解决
```

注意：这里**不是**在链路上加一个会转换 Markdown 的 HTTP 服务。那样等于把渲染塞进了传输层，既违背分层原则，也引入了缓存、端口、断线等额外故障点。

---

## 二、前置准备

假设环境如下，后续命令按此替换：

```text
Linux server  = 10.10.10.20
user          = lyy
文档目录      = /home/lyy/docs
Mac 挂载点    = ~/mnt/remote-docs
```

本机实测用的真实环境（2026-09-10 验证通过，命令与结论均以此为准）：

```text
Linux server  = hhdev（117.50.113.135，SSH 端口 12880）
user          = lyy
文档目录      = /data/lyy/taishan/docs
Mac 挂载点    = ~/mnt/remote-docs
```

### 2.1 安装 macFUSE 与 SSHFS

两者独立安装，缺一不可：

```bash
brew trust gromgit/fuse          # Homebrew 6 需先信任该 tap，否则 tap 会直接失败
brew install --cask macfuse
brew install gromgit/fuse/sshfs-mac
```

macFUSE 属于系统级扩展，安装后需要在「系统设置 → 隐私与安全性」里允许开发者 **Benjamin Fleischer** 的系统软件，然后重启一次；否则 `sshfs` 挂载会失败，并弹出「来自开发者 Benjamin Fleischer 的系统软件已被阻止载入」。

重启之后 kext **不会自动出现**，需要按需加载。macFUSE 自带入口可以自查和手动加载：

```bash
MACFUSE=/Library/Filesystems/macfuse.fs/Contents/Resources/macfuse.app/Contents/MacOS/macfuse
"$MACFUSE" kernel-extension check      # 输出 Kernel extension not loaded 表示尚未加载
sudo "$MACFUSE" kernel-extension load  # 手动加载；重跑挂载命令通常也会触发，但本机未单独验证
kmutil showloaded | grep -i macfuse    # 期望看到 io.macfuse.filesystems.macfuse.25
```

### 2.2 版本搭配注意事项（重要）

**不要看到“最新版”就无脑全升**，macFUSE 与 SSHFS 必须作为一组一起确认：

- macFUSE 官方仍在维护，5.3.3 是稳定版；5.4.0（2026-09-07）是开发预览版，不建议直接用于日常。
- SSHFS 官方当前版本为 3.7.5，Homebrew tap（`gromgit/fuse/sshfs-mac`）已提供 3.7.6。
- **已知回归问题**：SSHFS 3.7.5 在 macFUSE **5.3.3** 上可能无法正常挂载，降回 **5.3.2** 即可恢复。该问题在 macFUSE issue tracker（issue #1180）中仍是 open 状态。

本机在 `macFUSE 5.3.3 + SSHFS 3.7.6` 上实测到该回归的两种具体表现，都可以在命令层绕开：

| 表现 | 触发条件 | 处理 |
| --- | --- | --- |
| `sshfs` 执行后**不返回**，一直停在前台 | 该版本组合下 sshfs 不再自行后台化 | 命令加 `-f`，并放到 tmux / 后台运行（见步骤 2） |
| `ls` / `find` 触发断言 `Assertion failed: (offset == 0), function sftp_readdir_async, file sshfs.c, line 2321`，随后挂载变死挂载（`Device not configured`） | 使用 `-o dir_cache=no` | 去掉 `dir_cache=no`，改用默认目录缓存 + `-o dcache_timeout=5`（见步骤 2） |

结论：如果挂载报错或直接挂不上，先检查版本这一组搭配。**不一定要靠版本回退解决**——按步骤 2 的命令调整即可正常使用；若想彻底规避这个回归，再降回 `macFUSE 5.3.2`。

---

## 三、具体执行步骤

### 步骤 1：创建本地挂载点

```bash
mkdir -p ~/mnt/remote-docs
```

挂载点必须是 Mac 本地的一个空目录，挂载后目录内容会被远端目录“覆盖”显示。

### 步骤 2：挂载远端文档目录

推荐的基础挂载命令（面向“只读阅读、目录常变”的场景）：

```bash
# 该版本组合下 sshfs 不会自行后台化，所以必须加 -f 并放在 tmux / 后台里跑
sshfs lyy@10.10.10.20:/home/lyy/docs ~/mnt/remote-docs \
  -f \
  -o reconnect \
  -o volname=remote-docs \
  -o noappledouble \
  -o dcache_timeout=5
```

本机实测通过的命令（对照替换成真实环境）：

```bash
sshfs lyy@117.50.113.135:/data/lyy/taishan/docs ~/mnt/remote-docs \
  -f \
  -o reconnect \
  -o volname=remote-docs \
  -o noappledouble \
  -o dcache_timeout=5 \
  -p 12880
```

各参数作用：

| 参数 | 作用 | 为什么在你的场景里需要 |
| --- | --- | --- |
| `lyy@10.10.10.20:/home/lyy/docs` | 远端 `用户@主机:目录` | 指定要映射的 Linux 目录，即文档产生端 |
| `~/mnt/remote-docs` | 本地挂载点 | 之后 Mac 侧统一用这个路径访问 |
| `-f` | 前台运行，不尝试后台化 | macFUSE 5.3.3 下 sshfs 的后台化已损坏，不加会卡住不返回；配合 tmux / 后台执行 |
| `-o reconnect` | 断线后自动重连 | 网络抖动或休眠恢复后，尽量自动恢复挂载，避免手动重挂 |
| `-o volname=remote-docs` | 设置 Finder 中显示的卷名 | 让 Finder 侧边栏显示的是一眼能认出的名字，而不是长命令 |
| `-o noappledouble` | 禁止生成 AppleDouble 元数据文件 | 保证远端目录不被写入 `._xxx` 这类文件，维持“原封不动” |
| `-o dcache_timeout=5` | 目录项缓存 5 秒超时 | 兼顾“远端一改，本地可见”（实测 1 秒内可见）与稳定性；**不要用 `dir_cache=no`** |

关于 `noappledouble` 的定位：它不只是“减少垃圾文件”，更是分层原则的一部分——传输层不应该往远端目录里写入任何东西，包括 macOS 的元数据文件。

关于 `dir_cache` 的补充说明：

SSHFS 默认会缓存目录项和文件属性，所以“Linux 新建文件”与“Mac Finder 看到新文件”不是严格实时同步的。SSHFS 官方提供了一组缓存控制参数：

```text
dir_cache
dcache_timeout
dcache_stat_timeout
dcache_link_timeout
dcache_dir_timeout
```

第一版曾用 `-o dir_cache=no` 追求“随时看到最新文件”，但实测在 `macFUSE 5.3.3 + SSHFS 3.7.6` 上会触发 sshfs 断言崩溃（`sftp_readdir_async`，`offset == 0`），挂载随即变成死挂载，反而更不可靠。正确做法是保留默认目录缓存、把超时压短：

```text
-o dcache_timeout=5      # 目录项缓存 5 秒，实测远端新建文件 1 秒内本地可见
```

对于几十到几百个 Markdown / HTML 的文档目录，这点缓存不会造成实际困扰，而它能避开上面那个崩溃。

### 步骤 3：打开 HTML

HTML 最简单，映射后就是一个普通本地文件：

```bash
open ~/mnt/remote-docs/report.html
open ~/mnt/remote-docs/network/ovn.html
```

对应浏览器地址形如：

```text
file:///Users/<你的用户名>/mnt/remote-docs/report.html
```

HTML 内部的相对路径以**该 HTML 文件所在目录**为基准，所以整个远程文档目录可以直接当作一个“小网站”用：

```html
<img src="./images/topology.png">
<link rel="stylesheet" href="./style.css">
<script src="./app.js"></script>
```

这些引用都会被正常解析到挂载目录里的对应文件。这一层完全在浏览器内部完成，传输链路不参与。

### 步骤 4：渲染 Markdown（渲染在读取端解决）

按分层原则，Markdown 的渲染放在**读取端**，文件本身始终保持 `.md` 原样。

**首选：给浏览器装 Markdown 查看插件**

原理：插件充当“读取端渲染器”。浏览器打开 `.md` 时，插件把这份纯文本渲染成网页。文件内容和传输链路都不参与转换，SSHFS 依旧只搬字节。

操作步骤：

1. 安装 Markdown 查看插件。Chromium 系（Chrome / Edge）可用 Markdown Viewer 类扩展；Firefox 有对应的 add-on；Safari 取决于扩展生态。
2. **开启“允许访问文件网址”（Allow access to file URLs）**。`file://` 属于受限协议，不打开这个开关，插件不会处理本地 `.md`。
3. 正常打开：

```bash
open ~/mnt/remote-docs/notes/sriov.md
```

浏览器地址形如 `file:///Users/<用户名>/mnt/remote-docs/notes/sriov.md`，插件会把它渲染成网页。

需要注意的坑：

- 部分插件在渲染时会把页面的基准 URL 换成插件自身的 origin，导致 `.md` 里的相对图片 `![](./images/a.png)` 失效。若出现这种情况，改用相对文档根或绝对 `file://` 路径，或改用下面的备选方案。
- 不同插件对 `file://`、代码高亮、目录（TOC）的支持不一致，务必用你实际的文档集先测一遍。

**备选：在源端把 Markdown 转成 HTML**

渲染也可以前置到 **Linux 源端**，这样转换同样不发生在传输链路：

```bash
pandoc test.md -o test.html
open ~/mnt/remote-docs/test.html
```

产物 `.html` 只是一个普通文件，SSHFS 依旧原封不动搬运。适合希望“阅读端零依赖、不装插件”的情况，代价是源端要维护一份转换步骤。

不建议的做法：在 Mac 上跑一个会**转换** Markdown 的 HTTP 服务，再让浏览器访问 `http://127.0.0.1:8080/`。那等于把渲染塞进链路中间，和“传输层只搬字节”的分层冲突。如果确实要走 HTTP，也应让服务端只做**静态字节转发**（不转换），渲染仍交给浏览器。

### 步骤 5（可选）：封装一个 `docopen` 快捷命令

把“SSH 到 Linux 生成 → Mac 打开”的重复操作收敛成一条命令：

```bash
docopen() {
  open "$HOME/mnt/remote-docs/$1"
}
```

日常用法：

```bash
docopen network/ovn.html     # HTML
docopen notes/sriov.md       # Markdown（交给浏览器插件渲染）
```

也可以先给挂载点本身配个别名：

```bash
alias docs='open ~/mnt/remote-docs'
```

### 步骤 6（长期使用再考虑）：自动挂载

如果希望开机或登录后自动挂载，可以把上面的 `sshfs` 命令做成 launchd 任务或登录脚本。建议先用前几步手动跑通、确认版本搭配无误之后，再自动化，避免挂载失败被隐藏。

注意：因为该版本组合下 sshfs 不会自行后台化，自动化时应让 `sshfs -f` 作为**长期存活的进程**运行（由 launchd / tmux 托管），而不是写成会立刻返回的短命令；否则命令虽然“成功”，挂载点拿不到有效会话。

### 步骤 7：卸载

正常卸载：

```bash
umount ~/mnt/remote-docs
```

若提示资源忙或卡住，可用强制卸载：

```bash
diskutil unmount force ~/mnt/remote-docs
```

如果 sshfs 进程异常退出，`mount` 表里会残留**死挂载**：`ls ~/mnt/remote-docs` 报
`Device not configured`，但 `mount | grep remote-docs` 仍能看到条目。实测这种状态下直接执行 `umount` 就能清掉（`diskutil unmount force` 反而会报 `Unmount failed`）：

```bash
umount ~/mnt/remote-docs
mount | grep remote-docs || echo "挂载项已清除"
ls -la ~/mnt/remote-docs        # 应恢复为空目录
```

清掉后重新执行步骤 2 的挂载命令即可，不需要重启。

断开 SSH 连接后挂载会失效，文件也不会占用 Mac 磁盘空间。

---

## 四、整体架构回顾（推荐落地形态）

```text
                 Linux Server
                      │  源端只生成原始文件
                 SSH / SFTP
                      │  字节透明
                      ▼
                 macOS SSHFS
                      │  只搬运
                      ▼
                 ~/mnt/remote-docs
                    /        \
                   /          \
              .html           .md
                │               │
                ▼               ▼
        浏览器原生渲染    浏览器 Markdown 插件
        (file://)          （读取端渲染）
                \               /
                 \             /
                  ▼           ▼
                     浏览器窗口
```

优化组合：

```text
SSHFS 负责原封不动地把文件搬过来
        +
HTML 由浏览器原生渲染（file://）
        +
Markdown 由浏览器插件在读取端渲染
        +
Mac 上一条 docopen 命令
```

这样就能做到 **“Linux 上生成，Mac 浏览器秒开”**，同时传输层始终干净、无副作用。

### 另一条可选传输路线：SSH 隧道 + 源端静态服务

如果不想挂载文件系统，可以换成另一种**传输方式**：在 Linux 上跑一个只做**静态字节转发**的 HTTP 服务，Mac 通过 SSH 隧道访问 `localhost`。

```text
Mac 浏览器 ──http://127.0.0.1:8080──▶ SSH 隧道 ──▶ Linux 静态服务 ──▶ ~/docs
```

它绕开了 FUSE 缓存、Finder、文件属性等问题，但请注意两个前提：服务端**不转换内容**（渲染仍由浏览器完成），且 Mac 侧不再有“本地文件”，必须通过浏览器访问。若服务端会动态转换 Markdown，就回到了前面所说的“把渲染塞进链路”的问题。

---

## 五、测试方法

按顺序逐层验证，任一步失败即可定位问题所在层。

### 测试 1：挂载是否成功

先确认内核扩展这一层：

```bash
kmutil showloaded | grep -i macfuse    # 期望 io.macfuse.filesystems.macfuse.25
```

若没有输出，说明 kext 尚未加载（重启后不会自动出现），按 2.1 的 `kernel-extension load` 手动加载后再继续。

```bash
mount | grep remote-docs
ls -la ~/mnt/remote-docs
```

预期：能看到 `remote-docs` 的挂载项，且列出远端目录下的文件。若 `ls` 报错或为空，问题在挂载层（macFUSE / SSHFS / 版本搭配）。

### 测试 2：文件是否“原封不动”

在 Linux 上记录校验值：

```bash
md5sum /home/lyy/docs/report.md
```

在 Mac 上比对：

```bash
md5 -r ~/mnt/remote-docs/report.md
```

预期：两端哈希一致，说明传输层没有改动内容。同时确认远端目录里没有多出 `._xxx` 之类的文件（验证 `noappledouble`）。

### 测试 3：HTML 是否可直接打开

```bash
open ~/mnt/remote-docs/report.html
```

预期：默认浏览器打开并正确渲染页面。同时确认 CSS / 图片 / JS 这类相对路径资源也能加载，验证文档目录确实可以当作一个“小网站”。

### 测试 4：Markdown 渲染是否正常（读取端渲染）

```bash
open ~/mnt/remote-docs/notes/sriov.md
```

预期：浏览器通过 Markdown 插件渲染成网页，代码块、图片、目录正常。

对照验证（确认渲染确实来自插件，而非浏览器本身）：

```bash
# 临时停用 Markdown 插件，或关闭“允许访问文件网址”
open ~/mnt/remote-docs/notes/sriov.md
```

预期：此时应显示纯文本或触发下载，说明渲染能力来自读取端插件，文件本身始终是原始 `.md`。

### 测试 5：远程文件变化能否被本地看到

在 Linux 上操作：

```bash
vim /home/lyy/docs/report.md        # 修改并保存
touch /home/lyy/docs/new_file.html  # 新建文件
```

在 Mac 上：

```bash
ls -la ~/mnt/remote-docs           # 是否出现 new_file.html
open ~/mnt/remote-docs/report.html # 浏览器刷新后是否看到新内容
```

预期：

- 新建文件能出现在 `~/mnt/remote-docs`（`-o dcache_timeout=5`，本机实测 1 秒内即可见；缓存设得更长则需等待超时）。
- HTML 修改后刷新浏览器即可看到新内容。

### 测试 6：断线重连与稳定性

让 Mac 睡眠 / 断网再恢复，或临时中断网络，然后检查：

```bash
ls ~/mnt/remote-docs           # 挂载是否还能用、文件是否仍可读
mount | grep remote-docs   # reconnect 是否生效
```

预期：短暂断网恢复后，凭借 `-o reconnect` 挂载可以继续使用；若无法恢复，重新执行挂载命令。

### 测试 7：卸载与残留检查

```bash
umount ~/mnt/remote-docs
ls -la ~/mnt/remote-docs       # 应恢复为空目录
```

并在 Linux 上确认远端目录里**没有**出现 `._xxx` 之类的 AppleDouble 文件（验证 `noappledouble` 生效）。

---

## 六、常见问题速查

| 现象 | 可能原因 | 处理 |
| --- | --- | --- |
| 系统弹「来自开发者 Benjamin Fleischer 的系统软件已被阻止载入」 | kext 未获批准 | 系统设置 → 隐私与安全性 → 允许，然后重启 |
| 重启后 `sshfs` 挂载失败、`kmutil showloaded` 看不到 macfuse | kext 按需加载，重启后不会自动出现 | `sudo macfuse kernel-extension load` 手动加载后再挂载 |
| `sshfs` 执行后一直不返回、停在前台 | macFUSE 5.3.3 下 sshfs 不再自行后台化（issue #1180） | 命令加 `-f`，放到 tmux / 后台运行 |
| `ls` / `find` 触发 `Assertion failed: (offset == 0), function sftp_readdir_async` 后挂载变 `Device not configured` | 使用了 `-o dir_cache=no` | 去掉该参数，改用 `-o dcache_timeout=5` |
| 5.3.3 上仍有其它挂载异常 | macFUSE 5.3.3 回归问题 | 降到 macFUSE 5.3.2 |
| `brew install gromgit/fuse/sshfs-mac` 报 tap 不可信 / `invalid syntax in tap` | Homebrew 6 默认不信任第三方 tap | 先执行 `brew trust gromgit/fuse` |
| Linux 新建文件 Mac 看不到 | 目录缓存未超时 | 使用 `-o dcache_timeout=5`，或等待缓存超时 |
| `.md` 打开仍是纯文本 / 下载 | Markdown 插件未启用，或未开“允许访问文件网址” | 启用插件并打开 file URL 访问权限 |
| `.md` 里的图片不显示 | 插件改变了页面基准 URL，相对路径失效 | 改用相对文档根 / 绝对 `file://` 路径，或源端转 HTML |
| 远端目录出现 `._` 文件 | AppleDouble 元数据 | 挂载参数加 `-o noappledouble` |
| 卸载报设备忙 | 有进程占用挂载点 | `diskutil unmount force ~/mnt/remote-docs` |
| `ls` 报 `Device not configured`、`mount` 里却有残留条目 | sshfs 异常退出导致死挂载 | `umount ~/mnt/remote-docs`（实测 `diskutil unmount force` 会报 `Unmount failed`） |

---

## 七、本机实测记录（2026-09-10）

环境：macOS 26.0.1 (25A362) / Apple Silicon / macFUSE 5.3.3 / SSHFS 3.7.6 / Homebrew 6.0.6
远端：hhdev = `lyy@117.50.113.135:12880`，文档目录 `/data/lyy/taishan/docs`

| 验证项 | 判据 | 实测结果 |
| --- | --- | --- |
| kext 加载 | `kmutil showloaded \| grep -i macfuse` | `io.macfuse.filesystems.macfuse.25 (5.3.3)` |
| 挂载成功 | `mount \| grep remote-docs` | 显示 `macfuse` 类型挂载项，目录条目正常 |
| 内容逐字节一致 | 远端 `md5sum` vs 本地 `md5 -r` | 均为 `1030a2e0c89b06f3967725e8b184bd1a`（apiserver.md） |
| 无元数据污染 | 远端 `find ... -name "._*"` | `._` 文件数 0（`noappledouble` 生效） |
| 远端改动可见 | 远端 `touch` 新文件后本地 `ls` | 1 秒内可见；远端追加内容本地立即可读 |
| 全目录遍历 | `find ~/mnt/remote-docs -type f \| wc -l` | 128 个文件正常返回，无 `sftp_readdir_async` 断言 |
| 死挂载清理 | sshfs 异常退出后 `umount ~/mnt/remote-docs` | 挂载项清除，挂载点恢复为空目录，无需重启 |

未覆盖：HTML 渲染（该文档目录里没有 `.html` 文件）、`.md` 浏览器渲染（需自行安装 Markdown 插件并允许 file URL，见步骤 4）。

---

## 参考

- macFUSE 官网：https://macfuse.github.io/
- macFUSE SSHFS wiki：https://github.com/macfuse/macfuse/wiki/File-Systems-%E2%80%90-SSHFS
- SSHFS 官方文档（含缓存参数说明）：https://github.com/libfuse/sshfs/blob/master/sshfs.rst
- rclone mount：https://rclone.org/commands/rclone_mount/
- macFUSE 5.3.3 SSHFS 回归 issue #1180：https://github.com/macfuse/macfuse/issues/1180
- VS Code Remote 故障排查（含 macOS 安装方法）：https://github.com/microsoft/vscode-docs/blob/main/docs/remote/troubleshooting.md
