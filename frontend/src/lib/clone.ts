// clone deep-copies plain JSON data (campaign rules and content). It goes
// through JSON because structuredClone rejects Vue's reactive proxies, which
// can sit at any depth once an edited object is spread into a new one.
export function clone<T>(v: T): T {
  return JSON.parse(JSON.stringify(v)) as T
}
