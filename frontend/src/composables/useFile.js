import { ref } from 'vue'
import * as fileApi from '../api/file'

export function useFile() {
  const currentFile = ref(null)
  const fileContent = ref('')
  const loading = ref(false)

  async function openFile(path) {
    loading.value = true
    try {
      const content = await fileApi.readFile(path)
      currentFile.value = path
      fileContent.value = content
    } catch (e) {
      console.error('Failed to open file:', e)
    } finally {
      loading.value = false
    }
  }

  return { currentFile, fileContent, loading, openFile }
}
