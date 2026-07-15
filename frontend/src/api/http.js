/**
 * Event-based API client.
 * RPC calls use Wails compiled bindings directly.
 * This module exports bridge for event listening only.
 */
import bridge from '../utils/bridge'

export default bridge
export { bridge }
