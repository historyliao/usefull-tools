# SSH Mount Manager

macOS 上管理 sshfs 挂载的小工具：SwiftUI 原生 app（窗口 + 菜单栏）＋ 一套无界面 CLI，两者共用同一个引擎。

## 依赖

```bash
brew trust gromgit/fuse          # Homebrew 6 需先信任该 tap
brew install --cask macfuse
brew install gromgit/fuse/sshfs-mac
```

macFUSE 装完要在「系统设置 → 隐私与安全性」允许开发者 **Benjamin Fleischer** 的系统软件并重启；重启后 kext 不会自动出现，需要时手动加载：

```bash
MACFUSE=/Library/Filesystems/macfuse.fs/Contents/Resources/macfuse.app/Contents/MacOS/macfuse
"$MACFUSE" kernel-extension check
sudo "$MACFUSE" kernel-extension load
```

## 构建 / 安装 / 升级 / 卸载

```bash
cd ssh-mount-manager

make build          # 只构建到 /tmp/ssh-mount-manager-build（刻意不落在被索引的目录，不进启动台）
make install        # 构建 + 停旧实例 + 安装到 /Applications + 注册，并移除仓库内的构建副本
make run            # 等价于 make install 后打开 /Applications 里的那份
make cli            # 安装后用无界面模式列出所有挂载
```

说明：只有 `/Applications` 那一份会被注册进 LaunchServices。构建产物默认输出到 `/tmp/ssh-mount-manager-build/`（该目录不被 Spotlight 索引，且 `build.sh` 结束时会主动注销），避免启动台出现第二份同名应用。

### 升级流程（重要）

`/Applications/SSH Mount Manager.app` 是一份**完整拷贝**，不是快捷方式，与仓库没有运行时依赖。因此：

| 你做的事 | `/Applications` 里那份的表现 |
| --- | --- |
| 只跑 `make build`（重构仓库副本） | 不受影响，照旧能点开，但仍是**旧版本** |
| 跑 `make install` | 覆盖为新版本（会先停掉运行中的旧实例），启动台图标与位置不变 |
| 删除或移动仓库目录 | 不受影响，仍能启动 |
| 手动替换 bundle 里的可执行文件 | 会破坏 ad-hoc 签名，可能直接打不开 |

所以升级就是两步：

```bash
make install        # 1. 构建并覆盖安装
# 2. 从启动台 / 聚焦（⌘空格 搜 "SSH Mount"）重新点开
```

### 卸载

把 `/Applications/SSH Mount Manager.app` 拖进废纸篓即可。配置与日志在 `~/.ssh-mount-manager/`，需要彻底清理时一并删除。

注意：**不要同时运行仓库副本和 `/Applications` 副本**。两者 bundle id 相同、读同一份配置，同时管理会互相把对方启动的挂载识别成“外部”。

## 使用

- 主窗口：表格列出挂载（名称 / SSH 目标 / 远端路径 / 本地挂载点 / 状态 / 操作），每行自带「挂载 / 卸载」按钮
- 工具栏：添加、挂载/卸载、编辑、删除、在 Finder 打开、日志文件、强制清理、刷新
- 保存新条目后会自动选中；**编辑正在运行的条目会按新定义自动重挂**（先按旧定义卸载，再按新定义挂载）
- 菜单栏图标：对每条挂载一键挂载/卸载，另可打开主窗口
- 「退出时卸载全部」默认勾选，取消勾选则退出后挂载保留
- SSH 目标支持 `user@host`，也支持 `~/.ssh/config` 里的别名（如 `lyy@hhdev`）
- 挂载点在 Finder 里是**普通目录**而不是“卷”（DiskArbitration 不注册它），用 `⌘⇧G` 输入路径，或把目录拖进 Finder 侧边栏收藏

## CLI

```bash
BIN="/Applications/SSH Mount Manager.app/Contents/MacOS/SSHMountManager"

"$BIN" --cli list
"$BIN" --cli add --name taishan-docs --target lyy@hhdev --port 12880 \
       --remote /data/lyy/taishan/docs --mountpoint ~/mnt/remote-docs --identity ~/.ssh/id_rsa
"$BIN" --cli edit --name taishan-docs --remote /data/lyy/taishan/docs2
"$BIN" --cli mount <name> | unmount <name> | force-unmount <name> | remove <name>
```

## 文件位置

```text
~/.ssh-mount-manager/
  ├── mounts.json      挂载定义
  ├── events.log       操作与结果（mount/unmount/edit/force 的请求与结果）
  └── logs/<名称>.log   每条挂载的 sshfs 输出
```

## 原理

```text
ls ~/mnt/remote-docs
   ↓
macOS 内核 VFS
   ↓  macFUSE 内核扩展（只做桥接）
sshfs 进程（用户态文件系统，必须常驻）
   ↓  把 readdir/read/write 翻译成 SFTP
ssh -s ... sftp → 远端 sshd → 远端目录
```

- 传输层字节透传，Mac 上不落文件，内容与远端逐字节一致
- app 通过 `Process` 托管 sshfs 子进程；状态用内核接口 `getmntinfo()` 读取（不 spawn 子进程，卷无响应也不会拖死界面）
- 卸载顺序：`SIGTERM` sshfs → `umount` → `diskutil unmount force`；「强制清理」会先杀掉对应 sshfs 再走这条链
- 固定注入参数：`-f`、`-o reconnect`、`-o noappledouble`、`-o dcache_timeout=5`、`-o IdentityFile=...`
- `/Applications` 里的是完整拷贝，运行只依赖系统框架（SwiftUI / AppKit / 系统 Swift 库），与仓库无运行时依赖

## 已知坑（macFUSE 5.3.3 + sshfs 3.7.x）

- sshfs 不会自行后台化，必须 `-f` 并由 app / tmux 托管，否则命令不返回
- 不要使用 `-o dir_cache=no`，会触发 `sftp_readdir_async` 断言崩溃并留下死挂载；用 `-o dcache_timeout=5` 兼顾时效与稳定
- 卷卡死（`Device not configured`、`mount` 阻塞）时用界面里的「强制清理」或 `--cli force-unmount <name>`，无需重启
- 启动台里出现两份「SSH Mount Manager」：说明除 `/Applications` 外的构建副本被 Spotlight 索引并自动注册进了 LaunchServices。处理：`lsregister -u "<多余副本路径>"` → 移走该副本 → `killall Dock` 刷新启动台。构建产物本就不应放在被索引的目录里（`make build` 默认输出到 `/tmp/ssh-mount-manager-build`）

## 相关文档

- [mac-visit-linux-file.md](mac-visit-linux-file.md)：命令行手工挂载的完整流程、验证方法与坑位记录（本文档是 App 版的使用说明）
