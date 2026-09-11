import "./content.css";
import { renderMarkdown } from "./render.js";
import { enhance } from "./enhance.js";

const MARKDOWN_PATH = /\.(md|markdown|mdown|mkd)$/i;
const ENHANCE_LIMIT = 5 * 1024 * 1024;

function markdownSource() {
  const type = document.contentType ?? "";
  if (type !== "text/plain" && type !== "text/markdown") return null;
  if (!MARKDOWN_PATH.test(location.pathname)) return null;
  const body = document.body;
  // Chrome 的纯文本查看器把整个文件塞进单个 <pre>；HTML 页面（例如 GitHub 的 blob 页）到这里就被挡掉了。
  if (!body || body.children.length !== 1) return null;
  const only = body.firstElementChild;
  if (!only || only.tagName !== "PRE") return null;
  return only.textContent ?? "";
}

function requestStyles() {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, 1500);
    try {
      chrome.runtime.sendMessage({ type: "md-viewer:styles" }, () => {
        clearTimeout(timer);
        void chrome.runtime.lastError;
        resolve();
      });
    } catch {
      // 扩展被重新加载时 channel 会断，忽略即可
      clearTimeout(timer);
      resolve();
    }
  });
}

function fileName() {
  const raw = location.pathname.split("/").pop() ?? "";
  try {
    return decodeURIComponent(raw) || "Markdown";
  } catch {
    return raw || "Markdown";
  }
}

async function main() {
  const source = markdownSource();
  if (source === null) return;

  document.title = fileName();
  await requestStyles();

  const article = document.createElement("article");
  article.className = "markdown-body";
  article.innerHTML = renderMarkdown(source, location.href);

  document.documentElement.dataset.mdViewer = "1";
  document.body.replaceChildren(article);

  if (source.length > ENHANCE_LIMIT) {
    const notice = document.createElement("p");
    notice.className = "md-viewer-warning";
    notice.textContent = `文件约 ${(source.length / 1024 / 1024).toFixed(1)} MB，已渲染但未做数学/图表增强。`;
    article.prepend(notice);
    return;
  }
  await enhance(article);
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", main, { once: true });
} else {
  void main();
}
