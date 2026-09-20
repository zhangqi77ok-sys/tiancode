import * as monaco from 'monaco-editor'
import editorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker'

self.MonacoEnvironment = {
  getWorker() {
    return new editorWorker()
  }
}

// 遵循铁律 2: 代码暖炭黑 (#1E1C1A) 界面视觉规范与暖色极简
export function initMonacoThemes() {
  monaco.editor.defineTheme('tcode-warm-charcoal', {
    base: 'vs-dark',
    inherit: true,
    rules: [
      { token: '', background: '1E1C1A', foreground: 'D4D4D8' },
      { token: 'comment', foreground: '71717A', fontStyle: 'italic' },
      { token: 'keyword', foreground: 'D96B27', fontStyle: 'bold' },
      { token: 'string', foreground: '10A37F' },
      { token: 'number', foreground: 'F59E0B' },
      { token: 'type', foreground: '38BDF8' },
      { token: 'function', foreground: 'FB923C' },
    ],
    colors: {
      'editor.background': '#1E1C1A',
      'editor.foreground': '#D4D4D8',
      'editor.lineHighlightBackground': '#262320',
      'editorLineNumber.foreground': '#52525B',
      'editorLineNumber.activeForeground': '#D96B27',
      'editorCursor.foreground': '#D96B27',
      'editor.selectionBackground': '#D96B2735',
      'editor.inactiveSelectionBackground': '#D96B2720',
      'editorGutter.background': '#1A1816',
      'diffEditor.insertedTextBackground': '#10A37F25',
      'diffEditor.removedTextBackground': '#E11D4825',
      'diffEditor.diagonalFill': '#262320',
    }
  })
}

initMonacoThemes()
