# layout-discipline

[meta]

[description]
布局纪律核查：在编写或交付任何含滚动、数据表格、弹窗、输入框的界面前使用。用 RFC 2119 硬约束（MUST / MUST NOT / SHOULD / MAY）约束视口、滚动归属、弹窗与编辑区，防止溢出被掩盖、textarea 不撑满、表格依赖文档滚动。**MUST NOT 的违反应在代码审查阶段被标为阻塞项。**

[content]
# 布局纪律

## 一、基础约束：视口与滚动归属

在编写任何布局代码前，**MUST** 为当前页面/组件声明滚动模式，取值必须是其一：

- `single-screen-fit`：页面在标准视口内完整可见，无文档级滚动。
- `document-scroll`：允许文档整体上下滚动（**仅**用于展示型页面）。
- `panel-scroll`：文档不滚动，滚动发生在内部面板/表格/编辑器容器中。
- `data-overflow-exception`：数据表格或代码块允许水平滚动，但 **MUST** 声明例外范围。

**MUST NOT** 对 `html`、`body`、`#root`、`main` 或页面外壳使用 `overflow: hidden` 来掩盖布局溢出——溢出 **MUST** 修复根因。

**MUST NOT** 盲目使用整屏高度（`h-screen` / `height: 100vh`）。需要撑满视口时，用 `min-height: 100dvh`（带回退 `100vh`）或等效 `min-block-size`。

**MUST NOT** 在带内边距的包裹容器上使用 `100vw`——有滚动条时会产生水平溢出。

## 二、编辑区域：Textarea / 输入框撑满

**MUST** 当 `textarea` 位于弹窗、抽屉或面板中，且该容器有剩余垂直空间时，textarea 撑满该空间。实现 **MUST** 为 Flexbox：

```css
.editor-wrapper { display: flex; flex-direction: column; min-height: 0; }
.editor-wrapper > textarea,
.editor-wrapper .textarea-container { flex-grow: 1; min-height: 0; }
```

父容器 `min-height: 0` 允许 flex 子项收缩；子项 `flex-grow: 1` 自动填充剩余空间。这是标准且唯一被接受的方案，**MUST NOT** 用 JavaScript 动态计算高度。

**MUST NOT** 让 textarea 靠 `rows` 属性或固定像素高度适配，除非用户明确指定精确高度。

## 三、数据表格：内部滚动、固定列、吸顶表头

渲染数据表格（自研表格组件或等价 `table` / DataGrid）时，**MUST** 全部满足：

### 3.1 滚动归属

表格自身 **MUST** 是滚动容器，**MUST NOT** 依赖文档级滚动展示行数据。

表格组件 **MUST** 启用表头固定 + 内部垂直滚动（如设置明确的滚动高度，取值须为具体像素或可计算的视口表达式，**不允许**留空）。

### 3.2 固定列

列数超出容器宽度、需横向滚动时，**MUST** 将左侧标识列（行号/主键）与右侧操作列分别固定（`position: sticky` 或组件等价能力）。

**MUST** 为**所有列**显式指定宽度，仅允许一列不设宽以保持自适应；固定列必须有明确宽度。

**MUST NOT** 用 CSS 覆盖表格内部滚动容器的 `overflow` 行为（如强行给表体加 `overflow-y: auto`），会导致固定列错位。

### 3.3 表头与分页

表头 **MUST** 在表格内部垂直滚动时保持可见（吸顶/固定）。

分页控件 **SHOULD** 位于表格容器内部可见区或紧下方；**MUST NOT** 置于长列表最底部，迫使滚到底才能翻页。

### 3.4 水平滚动

文档级水平滚动 **MUST NOT** 因表格宽度触发；水平滚动 **仅**允许发生在表格自身滚动容器内。

## 四、弹窗（Modal / Dialog / Drawer）

**MUST** 为每个弹窗定义视口契约：

- **宽度守卫**：`max-width: min(92vw, [设计最大宽度])` 或等效表达式。
- **高度守卫**：`max-height: calc(100dvh - 32px)`，带回退值。
- **内部滚动所有者**：内容超过最大高度时，**MUST** 由弹窗 body 内部滚动，header 与 footer **MUST** 保持固定。

**MUST NOT** 仅用 `position: absolute; top: 50%; left: 50%; transform: translate(-50%,-50%)` 定位弹窗卡片而不加高度钳制——小视口高度（如 568px）下会溢出屏幕顶部。

弹窗打开时背景滚动 **MUST** 被锁定，但 **MUST NOT** 用 `overflow: hidden` 粗暴锁定整个页面外壳，应通过 `scroll-lock` 机制或焦点陷阱实现。

## 五、禁止清单

以下做法 **MUST NOT** 出现在任何 UI 代码中：

- 用 `overflow: hidden` 掩盖 `html`、`body`、`main`、页面外壳的溢出。
- 在带 padding 的容器上使用 `100vw`。
- 让内容密集容器使用 `100vh` 而不声明内部滚动所有者。
- 使用整屏高度而不带 `dvh` 或 `min-h` 回退。
- 用固定像素高度或 JS 动态计算"解决" textarea 撑满问题。
- 让数据表格依赖文档级滚动。
- 弹窗打开时允许背景滚动。
- 让表格宽度撑破文档导致水平页面滚动。

## 六、生效方式

约束关键词遵循 RFC 2119：**MUST** / **MUST NOT** / **SHOULD** / **MAY**；其中 `MUST NOT` 的违反应在代码审查阶段被标为**阻塞项**。
