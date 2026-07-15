import { GetNotes, GetNote, SaveNote, DeleteNote } from '../../wailsjs/go/main/App'

export function getNotes() {
  return GetNotes()
}

export function getNote(title) {
  return GetNote(title)
}

export function saveNote(title, content) {
  return SaveNote({ title, content })
}

export function deleteNote(title) {
  return DeleteNote(title)
}
