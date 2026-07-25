import { h } from 'vue'
import { dialog } from '../dialog'

export function confirm(message: string, title?: string): Promise<boolean> {
  return new Promise((resolve) => {
    let resolved = false

    const actionBar = h(
      'div',
      {
        style: {
          display: 'flex',
          justifyContent: 'flex-end',
          gap: '8px',
          marginTop: '20px',
        },
      },
      [
        h(
          'button',
          {
            class: 'b-btn b-btn--default b-btn--small',
            onClick: () => {
              resolved = true
              handle.close()
              resolve(false)
            },
          },
          '取消',
        ),
        h(
          'button',
          {
            class: 'b-btn b-btn--primary b-btn--small',
            onClick: () => {
              resolved = true
              handle.close()
              resolve(true)
            },
          },
          '确认',
        ),
      ],
    )

    const content = h(
      'div',
      {
        style: {
          padding: '8px 0',
        },
      },
      [
        h(
          'p',
          {
            style: {
              margin: 0,
              color: 'var(--text-secondary)',
              lineHeight: '1.6',
              fontSize: '14px',
            },
          },
          message,
        ),
        actionBar,
      ],
    )

    const handle = dialog.show(content, {
      title: title || '确认',
      width: 360,
      minimizable: false,
      collapsible: false,
      resizable: false,
      closable: true,
      onAction: (action) => {
        if ((action === 'close' || action === 'closed') && !resolved) {
          resolve(false)
        }
      },
    })
  })
}
