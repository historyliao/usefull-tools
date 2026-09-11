import renderMathInElement from "katex/dist/contrib/auto-render.mjs";
import mermaid from "mermaid";
import hljs from "highlight.js/lib/common";

let mermaidReady = false;

export function enhance(container, options = {}) {
  highlightCode(container);
  renderMath(container);
  if (options.mermaid !== false) {
    return renderMermaid(container);
  }
  return Promise.resolve();
}

function highlightCode(container) {
  for (const block of container.querySelectorAll("pre code")) {
    const match = /language-([\w+-]+)/.exec(block.className);
    const language = match?.[1];
    if (language === "mermaid") continue;
    if (language && hljs.getLanguage(language)) {
      block.innerHTML = hljs.highlight(block.textContent ?? "", { language, ignoreIllegals: true }).value;
    }
    block.classList.add("hljs");
  }
}

function renderMath(container) {
  renderMathInElement(container, {
    delimiters: [
      { left: "$$", right: "$$", display: true },
      { left: "\\[", right: "\\]", display: true },
      { left: "$", right: "$", display: false },
      { left: "\\(", right: "\\)", display: false },
    ],
    ignoredTags: ["script", "noscript", "style", "textarea", "pre", "code", "option"],
    throwOnError: false,
    errorColor: "#cc0000",
  });
}

async function renderMermaid(container) {
  const blocks = [...container.querySelectorAll("pre > code.language-mermaid")];
  if (blocks.length === 0) return;

  if (!mermaidReady) {
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: "strict",
      theme: window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "default",
      fontFamily: "inherit",
    });
    mermaidReady = true;
  }

  const nodes = blocks.map((code) => {
    const source = code.textContent ?? "";
    const wrapper = document.createElement("div");
    wrapper.className = "mermaid";
    wrapper.textContent = source;
    code.parentElement.replaceWith(wrapper);
    return wrapper;
  });

  try {
    await mermaid.run({ nodes, suppressErrors: true });
  } catch (error) {
    for (const node of nodes) {
      if (!node.querySelector("svg")) {
        node.classList.add("mermaid-failed");
        node.textContent = `Mermaid 渲染失败：${error?.message ?? error}`;
      }
    }
  }
}
