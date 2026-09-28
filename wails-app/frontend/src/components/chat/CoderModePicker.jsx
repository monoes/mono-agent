import { Bot, Code2, FolderPlus, FolderOpen, History } from 'lucide-react'
import { folderName, missingText } from './useCoderMode.js'

// CoderModePicker is the new-chat choice between the Assistant and Coder
// modes (#203), plus Coder's workspace: a new test folder (default), a
// folder picked with the native dialog, or a recent one. Shown only before
// a conversation exists, since its mode can't change once it starts. Renders
// nothing while coder mode is off in Settings.
//
// workspace is { kind: 'new' } or { kind: 'folder', path }.

const mono = 'var(--font-mono)'
const CYAN = '#00b4d8'

function segStyle(active, disabled) {
  return {
    flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 5,
    padding: '5px 8px', borderRadius: 6, font: 'inherit', fontFamily: mono, fontSize: 10, fontWeight: 600, letterSpacing: 0.5,
    cursor: disabled ? 'not-allowed' : 'pointer', opacity: disabled ? 0.45 : 1,
    background: active ? 'rgba(0,180,216,0.15)' : 'transparent',
    border: `1px solid ${active ? 'rgba(0,180,216,0.45)' : 'rgba(0,180,216,0.12)'}`,
    color: active ? CYAN : 'var(--text-muted)',
  }
}

function optionStyle(active) {
  return {
    display: 'flex', alignItems: 'flex-start', gap: 7, width: '100%', textAlign: 'left',
    padding: '6px 8px', borderRadius: 6, cursor: 'pointer', font: 'inherit',
    background: active ? 'rgba(0,180,216,0.1)' : 'transparent',
    border: `1px solid ${active ? 'rgba(0,180,216,0.35)' : 'transparent'}`,
    color: '#e2e8f0',
  }
}

const optTitle = { fontFamily: mono, fontSize: 10.5, color: '#e2e8f0' }
const optHint = { fontFamily: mono, fontSize: 9, color: 'var(--text-muted)', wordBreak: 'break-all', marginTop: 1 }

export function CoderModePicker({ status, mode, onModeChange, workspace, onWorkspaceChange, recent = [], onPickFolder, disabled = false }) {
  if (!status?.enabled) return null
  const ready = status.ready !== false
  const pickedPath = workspace?.kind === 'folder' ? workspace.path : ''
  const pickedIsRecent = recent.some(w => w.path === pickedPath)

  return (
    <div data-testid="coder-mode-picker" style={{ padding: '8px 12px', borderBottom: '1px solid rgba(0,180,216,0.06)', display: 'flex', flexDirection: 'column', gap: 6, flexShrink: 0 }}>
      <div role="radiogroup" aria-label="Chat mode" style={{ display: 'flex', gap: 6 }}>
        <button type="button" role="radio" aria-checked={mode === 'assistant'} disabled={disabled}
          onClick={() => onModeChange('assistant')} style={segStyle(mode === 'assistant', disabled)}>
          <Bot size={11} /> Assistant
        </button>
        <button type="button" role="radio" aria-checked={mode === 'coder'} disabled={disabled || !ready}
          title={ready ? 'Claude Code with full access inside one folder' : `Coder mode ${missingText(status)}`}
          onClick={() => onModeChange('coder')} style={segStyle(mode === 'coder', disabled || !ready)}>
          <Code2 size={11} /> Coder
        </button>
      </div>
      {!ready && (
        <div data-testid="coder-not-ready" style={{ fontFamily: mono, fontSize: 9.5, color: '#fbbf24', lineHeight: 1.5 }}>
          Coder mode {missingText(status)}.
        </div>
      )}

      {mode === 'coder' && ready && (
        <div role="radiogroup" aria-label="Coder workspace" style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          <button type="button" role="radio" aria-checked={workspace?.kind !== 'folder'} disabled={disabled}
            onClick={() => onWorkspaceChange({ kind: 'new' })} style={optionStyle(workspace?.kind !== 'folder')}>
            <FolderPlus size={12} color={CYAN} style={{ marginTop: 1, flexShrink: 0 }} />
            <span style={{ minWidth: 0 }}>
              <div style={optTitle}>New test folder</div>
              <div style={optHint}>A fresh folder in {status.workspaceRoot || 'the workspace root'}</div>
            </span>
          </button>
          <button type="button" role="radio" aria-checked={!!pickedPath && !pickedIsRecent} disabled={disabled}
            onClick={onPickFolder} style={optionStyle(!!pickedPath && !pickedIsRecent)}>
            <FolderOpen size={12} color={CYAN} style={{ marginTop: 1, flexShrink: 0 }} />
            <span style={{ minWidth: 0 }}>
              <div style={optTitle}>Choose folder…</div>
              <div style={optHint}>{pickedPath && !pickedIsRecent ? pickedPath : 'Any folder you trust'}</div>
            </span>
          </button>
          {recent.length > 0 && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 5, margin: '4px 0 1px 8px', fontFamily: mono, fontSize: 8.5, color: 'var(--text-muted)', letterSpacing: 1.5, textTransform: 'uppercase' }}>
              <History size={9} /> Recent
            </div>
          )}
          {recent.slice(0, 5).map(w => (
            <button key={w.path} type="button" role="radio" aria-checked={pickedPath === w.path} disabled={disabled}
              title={w.path} onClick={() => onWorkspaceChange({ kind: 'folder', path: w.path })} style={optionStyle(pickedPath === w.path)}>
              <span style={{ minWidth: 0, paddingLeft: 19 }}>
                <div style={optTitle}>{folderName(w.path)}</div>
                <div style={optHint}>{w.path}{w.conversations ? ` · ${w.conversations} chat${w.conversations === 1 ? '' : 's'}` : ''}</div>
              </span>
            </button>
          ))}
          <div style={{ fontFamily: mono, fontSize: 9, color: 'var(--text-muted)', lineHeight: 1.5, marginTop: 2 }}>
            Claude Code will run commands and change files in this folder without asking.
          </div>
        </div>
      )}
    </div>
  )
}
