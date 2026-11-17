import { useEffect, useState } from 'react'

export function NetworkNotice() {
  const [online, setOnline] = useState(navigator.onLine)
  useEffect(() => {
    const update = () => setOnline(navigator.onLine)
    window.addEventListener('online', update); window.addEventListener('offline', update)
    return () => { window.removeEventListener('online', update); window.removeEventListener('offline', update) }
  }, [])
  return online ? null : <div className="alert alert-error network-notice" role="alert">You appear to be offline. No changes are queued or retried automatically. Check records before repeating a pending change.</div>
}