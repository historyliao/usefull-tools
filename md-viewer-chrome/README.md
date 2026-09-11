# Markdown Viewer (GFM) — Chrome 扩展

在 Chrome 里就地渲染 Markdown，不改变 URL、不重新发请求：

| 能力 | 说明 |
| --- | --- |
| GFM | 表格、任务列表、删除线、自动链接、围栏代码、行内 HTML |
| 代码高亮 | highlight.js（common 语言集） |
| 数学公式 | KaTeX，支持 `$…$`、`$$…$$`、`\(…\)`、`\[…\]` |
| 图 | Mermaid 围栏代码块渲染成 SVG |
| 样式 | github-markdown-css，跟随系统亮/暗 |
| 链接 | 相对链接与图片按原文件 URL 解析；标题锚点与 GitHub 的 slug 规则一致 |

原理：Chrome 把 `.md` 当纯文本显示（`text/markdown`/`text/plain` + 单个 `<pre>`），
扩展在 `document_end` 判定命中后，**直接拿页面里已有的文本**渲染并把 `<pre>` 换成文章，因此
URL、书签、回退、私有 raw 链接的鉴权都保持原样。

## 安装

```bash
cd md-viewer-chrome
npm install
make build          # 产出 dist/
```

然后在 Chrome 里：

1. 打开 `chrome://extensions`，右上角打开「开发者模式」
2. 点「加载已解压的扩展程序」，选 **`md-viewer-chrome/dist`**
3. 在本扩展卡片上打开「**允许访问文件网址**」（看本地 `.md` 必须开，远程 raw 不需要）

## 用法

直接在地址栏打开就行，扩展会自动接管：

- 本地文件：`file:///Users/you/docs/README.md`（需先开上面的文件访问开关）
- raw 链接：`https://raw.githubusercontent.com/<user>/<repo>/<branch>/README.md`
- gist raw：`https://gist.githubusercontent.com/.../raw/.../x.md`
- 私有仓库的 raw（走浏览器已登录的 Cookie，扩展不再发第二次请求）
- 路径以 `.md` / `.markdown` / `.mdown` / `.mkd` 结尾且响应是纯文本的任意地址

## 不做什么

- **不接管 HTML 页面**：GitHub 的 `blob` 页面 URL 也以 `.md` 结尾，但它是 `text/html`，扩展一律不碰（已用 e2e 断言）。
- **不改超时下载的场景**：服务端给 `Content-Disposition: attachment` 或二进制类型时 Chrome 直接下载，页面根本没加载，扩展无从介入。
- **不渲染 MDX**：`.mdx` 含 JSX，按普通 Markdown 渲染会失真，因此不匹配该后缀。
- 大文件（约 5 MB 字符以上）会跳过 KaTeX/Mermaid 增强，只做基础渲染并在顶部提示。

## 开发

```bash
npm install
make build      # 构建到 dist/
make watch      # 改代码自动重建（改完在 chrome://extensions 点刷新）
make test       # 渲染管线单测（jsdom，8 个用例）
make e2e        # 真 Chrome 端到端（会起一个真窗口，跑完自动关）
make clean
```

两个已经踩过的坑，写在 `test/e2e.mjs` 里省得再踩：

1. **headless 不加载扩展**，所以 e2e 起的是真窗口，跑完自动杀掉；
2. **品牌版 Chrome 忽略 `--load-extension`**，所以 e2e 用 CDP 的 `Extensions.loadUnpacked`
   （需要 `--enable-unsafe-extension-debugging`），同时保留 `--load-extension` 兼容 Chromium。

e2e 覆盖：接管判定、GFM 表格、任务列表、代码高亮、KaTeX、Mermaid SVG、危险 HTML 清洗、
`<pre>` 被替换、标题取自文件名、HTML 页面不被干预。

## 目录

```text
md-viewer-chrome/
  src/manifest.json     MV3 清单
  src/content.js        探针 + 就地渲染
  src/render.js         markdown → 清洗后的 HTML（纯函数，可单测）
  src/enhance.js        代码高亮 / KaTeX / Mermaid（在已清洗的 DOM 上跑）
  src/content.css       github-markdown-css + hljs 主题 + katex.min.css + 自定义
  src/background.js     命中时给标签页注入样式；启动时提示文件访问开关
  scripts/build.mjs     esbuild 打包（IIFE，MV3 content script 不支持 ESM）
  test/render.test.mjs  渲染管线单测
  test/e2e.mjs          CDP 端到端
  test/fixtures/gfm.md  GFM 用例（含恶意 HTML）
```

## 安全

- 渲染前过 **DOMPurify**（`script`/`iframe`/`object`/`form`/`on*`/`javascript:` 全部剥掉），KaTeX 与 Mermaid 都只作用在**已清洗**的 DOM 上。
- 外链补 `rel="noopener noreferrer"` 与 `target="_blank"`；相对链接按原文件 URL 解析。
- 扩展不发起任何自己的网络请求、不收集数据；远程内容用的是浏览器本来那次导航。
- 权限只有 `scripting` + 三个 host 匹配（`http://*/*`、`https://*/*`、`file:///*`）——这也是安装时提示「读取和更改你在所有网站上的数据」的原因，个人自用可接受，若将来要上架需改成按站点授权。

## 依赖与许可

全部是开源库，构建时打进 `dist/`（MV3 不允许远程代码）：

| 依赖 | 用途 | 许可 |
| --- | --- | --- |
| marked | GFM 解析 | MIT |
| marked-gfm-heading-id | 与 GitHub 一致的标题锚点 | MIT |
| dompurify | HTML 清洗 | Apache-2.0 OR MPL-2.0 |
| highlight.js | 代码高亮 | BSD-3-Clause |
| katex | 数学公式 | MIT |
| mermaid | 图 | MIT |
| github-markdown-css | 样式 | MIT |
| esbuild / jsdom | 构建与测试（dev） | MIT |

`npm audit` 输出 0 漏洞；mermaid 传递依赖的 `lodash-es` 用 `overrides` 固定到 `^4.18.1`（避开 4.17.23 及以下的两个 high 公告）。

## 体积

`dist/` 约 6.7 MB，其中 `content.js` 5.8 MB（Mermaid + KaTeX 占大头），字体 60 个 woff2/woff/ttf。
这是"离线可用 + 支持公式与图"的直接代价；如果只想要 GFM，去掉这两个依赖可以降到 ~300 KB。
