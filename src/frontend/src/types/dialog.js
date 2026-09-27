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
}
