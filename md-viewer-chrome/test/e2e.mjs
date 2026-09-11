// 真 Chrome 端到端验证：装扩展 → 打开本地 HTTP 上的 .md → 用 CDP 读渲染结果。
//
// 两个坑：① headless 模式不加载扩展，所以这里起的是真窗口（跑完自动关）；
// ② 品牌版 Chrome 已经忽略 --load-extension，改用 CDP 的 Extensions.loadUnpacked。
//
//   node test/e2e.mjs
import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import assert from "node:assert/strict";

const CHROME = process.env.CHROME_BIN ?? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const EXT = new URL("../dist", import.meta.url).pathname;

const MARKDOWN = `# 端到端验证

| 列 | 值 |
| --- | --- |
| a | 1 |

- [x] 完成

\`\`\`go
fmt.Println("hi")
\`\`\`

行内公式 $a^2 + b^2 = c^2$

\`\`\`mermaid
graph LR
  A --> B
\`\`\`

<script>alert("xss")</script>
`;

const HTML_PAGE = `<!doctype html><html><head><title>真页面</title></head><body><h1>HTML</h1></body></html>`;

const server = createServer((request, response) => {
  const isHtml = request.url?.startsWith("/html.md");
  const body = Buffer.from(isHtml ? HTML_PAGE : MARKDOWN);
  response.writeHead(200, {
    "Content-Type": isHtml ? "text/html; charset=utf-8" : "text/plain; charset=utf-8",
    "Content-Length": body.length,
  });
  response.end(body);
});
await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
const base = `http://127.0.0.1:${server.address().port}`;

const profile = await mkdtemp(join(tmpdir(), "mdv-e2e."));
const chrome = spawn(
  CHROME,
  [
    `--user-data-dir=${profile}`,
    "--enable-unsafe-extension-debugging",
    `--load-extension=${EXT}`, // 非品牌版 Chromium 仍然认这个开关
    "--no-first-run",
    "--no-default-browser-check",
    "--remote-debugging-port=0",
    "about:blank",
  ],
  { stdio: "ignore" }
);

process.on("exit", () => chrome.kill("SIGKILL"));

async function debuggingPort() {
  const file = join(profile, "DevToolsActivePort");
  for (let i = 0; i < 100; i++) {
    try {
      const [port] = (await readFile(file, "utf8")).split("\n");
      if (port) return Number(port);
    } catch {}
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error("Chrome 没有写出调试端口");
}

function cdpCall(url, method, params = {}) {
  return new Promise((resolve, reject) => {
    const socket = new WebSocket(url);
    const id = 1;
    const timer = setTimeout(() => {
      socket.close();
      reject(new Error(`${method} 超时`));
    }, 20_000);
    socket.addEventListener("open", () => socket.send(JSON.stringify({ id, method, params })));
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(event.data);
      if (message.id !== id) return;
      clearTimeout(timer);
      socket.close();
      if (message.error) {
        reject(new Error(`${method} 失败: ${JSON.stringify(message.error)}`));
      } else {
        resolve(message.result);
      }
    });
    socket.addEventListener("error", () => {
      clearTimeout(timer);
      reject(new Error(`${method} 连接失败`));
    });
  });
}

async function evaluate(expression, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
      const page = targets.find((t) => t.type === "page" && t.url.startsWith(base));
      if (page) {
        const result = await cdpCall(page.webSocketDebuggerUrl, "Runtime.evaluate", {
          expression,
          returnByValue: true,
          awaitPromise: true,
        });
        if (result.exceptionDetails) throw new Error(result.exceptionDetails.text);
        return result.result?.value;
      }
    } catch (error) {
      lastError = error;
    }
    await new Promise((r) => setTimeout(r, 300));
  }
  throw lastError ?? new Error(`等待页面超时: ${expression}`);
}

async function waitFor(expression, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await evaluate(expression)) return true;
    await new Promise((r) => setTimeout(r, 300));
  }
  return false;
}

const port = await debuggingPort();
let failed = 0;
const check = (label, ok) => {
  console.log(`${ok ? "✓" : "✗"} ${label}`);
  if (!ok) failed++;
};

try {
  const version = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json();
  let loaded;
  try {
    loaded = await cdpCall(version.webSocketDebuggerUrl, "Extensions.loadUnpacked", { path: EXT });
  } catch (error) {
    console.warn(`Extensions.loadUnpacked 不可用（${error.message}），依赖 --load-extension`);
  }
  if (loaded?.id) console.log(`扩展已装载：${loaded.id}\n`);

  await cdpCall(version.webSocketDebuggerUrl, "Target.createTarget", { url: `${base}/README.md` });

  check("content script 接管了 text/plain 的 .md", await waitFor(`document.documentElement.dataset.mdViewer === "1"`));
  check("GFM 表格已渲染", (await evaluate(`document.querySelectorAll('.markdown-body table').length`)) === 1);
  check("任务列表已渲染", (await evaluate(`!!document.querySelector('input[type=checkbox]')`)) === true);
  check("代码高亮生效", (await evaluate(`!!document.querySelector('code.hljs .hljs-keyword, code.hljs .hljs-built_in, code.hljs span')`)) === true);
  check("KaTeX 公式已渲染", await waitFor(`document.querySelectorAll('.katex').length > 0`));
  check("Mermaid 图已渲染成 SVG", await waitFor(`document.querySelectorAll('.mermaid svg').length > 0`));
  check("危险 script 已清洗", (await evaluate(`document.querySelectorAll('script').length`)) === 0);
  check("原始 <pre> 已被替换", (await evaluate(`!document.querySelector('body > pre')`)) === true);
  check("标题取自文件名", (await evaluate(`document.title`)) === "README.md");

  await evaluate(`location.href = ${JSON.stringify(`${base}/html.md`)}`);
  await new Promise((r) => setTimeout(r, 1500));
  check(
    "URL 以 .md 结尾的 HTML 页面不被干预",
    (await evaluate(`document.documentElement.dataset.mdViewer === undefined && !!document.querySelector('h1')`)) === true
  );
} finally {
  chrome.kill("SIGKILL");
  server.close();
  await rm(profile, { recursive: true, force: true }).catch(() => {});
}

assert.equal(failed, 0, `${failed} 项端到端检查失败`);
console.log("\n端到端全部通过");
