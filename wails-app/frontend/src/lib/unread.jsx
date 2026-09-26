// An inbound message nobody has marked read yet (migration 050).
export const isUnread = m => m?.direction === 'inbound' && !m?.read_at

// The dot shown next to an unread message.
export function UnreadDot({ title = 'Unread' }) {
  return <span className="unread-dot" title={title} aria-label={title} />
}
