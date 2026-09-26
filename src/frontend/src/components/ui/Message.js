const iconMap = {
  info: 'ℹ️',
  success: '✅',
  error: '❌',
  warning: '⚠️',
}

function createContainer() {
  let container = document.getElementById('b-message-container')
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

function removeEl(el) {
  if (el.parentNode) {
    el.parentNode.removeChild(el)
  }
}

export function showMessage(text, type = 'info', duration = 3000) {
  const container = createContainer()
  const el = document.createElement('div')
  el.className = `b-message b-message--${type}`
  // Icon is a fixed emoji (safe); text is user-controlled so it is assigned
  // via textContent to prevent HTML injection (XSS).
  el.innerHTML = `<span class="b-message-icon">${iconMap[type]}</span><span class="b-message-text"></span>`
  const textEl = el.querySelector('.b-message-text')
  if (textEl) textEl.textContent = text
  el.style.cssText = `
    pointer-events: auto;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 10px 16px;
    border-radius: var(--border-radius);
    font-size: var(--font-size-sm);
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
  info: (text) => showMessage(text, 'info'),
  success: (text) => showMessage(text, 'success'),
  error: (text) => showMessage(text, 'error'),
  warning: (text) => showMessage(text, 'warning'),
}
