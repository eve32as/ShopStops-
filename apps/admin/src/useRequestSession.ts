import { useLayoutEffect, useMemo } from 'react'
import { RequestSession } from './requestSession.ts'

export function useRequestSession(gateway: string, token: string) {
  const identity = useMemo(() => ({ gateway, token, requests: new RequestSession() }), [gateway, token])
  const session = identity.requests
  useLayoutEffect(() => {
    session.activate()
    return () => session.close()
  }, [session])
  return { session }
}