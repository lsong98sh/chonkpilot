import { GetTasksByTurn, UpdateTaskStatus } from '../../wailsjs/go/main/App'

export function getTasksByTurn(turnId) {
  return GetTasksByTurn(turnId)
}

export function updateTaskStatus(taskId, status, progress, result) {
  return UpdateTaskStatus({ task_id: taskId, status, progress, result })
}
