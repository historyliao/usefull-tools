# GFM 示例

## 表格与任务列表

| 项目 | 状态 | 备注 |
| --- | --- | --- |
| 渲染 | ✅ | 表格 |
| 公式 | ✅ | $E = mc^2$ |

- [x] 已完成的任务
- [ ] 未完成的任务

## 代码与内联内容

```go
func main() {
	fmt.Println("hello")
}
```

按 <kbd>⌘</kbd>+<kbd>K</kbd> 打开命令面板，行内代码 `spm run`，删除线 ~~不要了~~。

## 链接与图片

- [同目录文档](./other.md)
- [外链](https://example.com/x)
- 自动链接 https://example.org/auto

![示例图](../images/demo.png)

## 数学与图表

块级公式：

$$
\int_0^1 x^2 \, dx = \frac{1}{3}
$$

```mermaid
graph LR
  A[远端] -->|ssh -R| B[本机]
```

## 恶意内容（必须被清洗）

<script>alert("xss")</script>

<img src=x onerror="alert(1)">

[危险链接](javascript:alert(1))
