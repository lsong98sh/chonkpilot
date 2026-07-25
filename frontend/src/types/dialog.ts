export type DialogAction =
  | 'open'
  | 'maximize'
  | 'minimize'
  | 'collapse'
  | 'restore'
  | 'resize'
  | 'move'
  | 'ok'
  | 'cancel'
  | 'close'
  | 'closed'

export interface DialogPosition {
  x: number
  y: number
}

export interface DialogSize {
  width: number
  height: number
}

export interface DialogOptions {
  title: string
  modal?: boolean
  resizable?: boolean
  minimizable?: boolean
  collapsible?: boolean
  closable?: boolean
  draggable?: boolean
  width?: number | string
  height?: number | string
  minWidth?: number
  minHeight?: number
  maxWidth?: string
  maxHeight?: string
  position?: DialogPosition
  zIndex?: number
  class?: string
  headerClass?: string
  bodyClass?: string
  onAction?: (action: DialogAction, payload?: any) => void
}

export interface DialogHandle {
  close(): void
  minimize(): void
  restore(): void
  collapse(): void
  expand(): void
  setTitle(title: string): void
  setOptions(opts: Partial<DialogOptions>): void
  getState(): 'normal' | 'maximized' | 'minimized' | 'collapsed'
}

export const DefaultDialogOptions: Partial<DialogOptions> = {
  modal: true,
  resizable: false,
  minimizable: false,
  collapsible: true,
  closable: false,
  draggable: true,
  width: 640,
  minWidth: 400,
  minHeight: 300,
  maxWidth: '90vw',
  maxHeight: '80vh',
}

export type DialogState = 'normal' | 'maximized' | 'minimized' | 'collapsed'
