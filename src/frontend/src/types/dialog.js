export const DefaultDialogOptions = {
  modal: true,
  resizable: false,
  minimizable: false,
  maximizable: false,
  collapsible: true,
  closable: false,
  draggable: true,
  width: 640,
  // 默认高度：未显式传 height 的弹窗直接落到该值（min(80vh, 640px)），
  // 消除「先按声明尺寸定位、内容渲染后再按实测高度重居中」的开窗跳动；
  // 调用方 options.height 可覆盖（瞬时确认/选择类弹窗传 'auto' 保持内容自适应）。
  height: 'min(80vh, 640px)',
  minWidth: 400,
  minHeight: 300,
  maxWidth: '90vw',
  maxHeight: '80vh',
  // 居中口径（AskUser S1-6，2026-10-29）：缺省 = **窗口内居中**（既有行为，零变化）；
  // true = **屏幕工作区居中**（整个桌面去掉任务栏的可用区正中；每次弹出重新居中，
  // 不记忆上次拖拽位置）。仅 ask 弹窗开启，见 docs/ask-user/AskUser专题设计方案.md §4.6。
  centerOnScreen: false,
}
