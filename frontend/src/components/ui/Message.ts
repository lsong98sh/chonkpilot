type MessageType = 'info' | 'success' | 'error' | 'warning'

const iconMap: Record<MessageType, string> = {
  info: 'ℹ️',
  success: '✅',
  error: '❌',
  warning: '⚠️',
}

function createContainer(): HTMLDivElement {
  let container = document.getElementById('b-message-container') as HTMLDivElement
  if (!container) {
    container = document.createElement('div')
    container.id = 'b-message-container'
    container.style.cssText = `
      position: fixed;
      top: 16px;
      left: 50%;
      transform: translateX(-50%);
      z-index: 9999;
      display: flex;
      flex-direction: column;
      align-items: center;
      gap: 8px;
      pointer-events: none;
    `
    document.body.appendChild(container)
  }
  return container
}

function removeEl(el: HTMLDivElement) {
  if (el.parentNode) {
    el.parentNode.removeChild(el)
  }
}

export function showMessage(text: string, type: MessageType = 'info', duration: number = 3000) {
  const container = createContainer()
  const el = document.createElement('div')
  el.className = `b-message b-message--${type}`
  el.innerHTML = `<span class="b-message-icon">${iconMap[type]}</span><span class="b-message-text">${text}</span>`
  el.style.cssText = `
    pointer-events: auto;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 10px 16px;
    border-radius: var(--border-radius, 4px);
    font-size: var(--font-size-sm, 13px);
    background: var(--bg-secondary, #fff);
    color: var(--text-primary, #212529);
    box-shadow: 0 4px 12px rgba(0,0,0,0.12);
    border: 1px solid var(--border, #dee2e6);
    transition: all 0.25s ease;
    opacity: 0;
    transform: translateY(-12px);
  `

  // Type-specific border accent
  if (type === 'success') {
    el.style.borderLeft = `3px solid var(--success, #2d9f4e)`
  } else if (type === 'error') {
    el.style.borderLeft = `3px solid var(--danger, #dc3545)`
  } else if (type === 'warning') {
    el.style.borderLeft = `3px solid var(--warning, #e8a317)`
  } else {
    el.style.borderLeft = `3px solid var(--accent, #4361ee)`
  }

  container.appendChild(el)

  // Animate in
  requestAnimationFrame(() => {
    el.style.opacity = '1'
    el.style.transform = 'translateY(0)'
  })

  // Auto remove
  setTimeout(() => {
    el.style.opacity = '0'
    el.style.transform = 'translateY(-12px)'
    setTimeout(() => removeEl(el), 250)
  }, duration)
}

export const message = {
  info: (text: string) => showMessage(text, 'info'),
  success: (text: string) => showMessage(text, 'success'),
  error: (text: string) => showMessage(text, 'error'),
  warning: (text: string) => showMessage(text, 'warning'),
}
