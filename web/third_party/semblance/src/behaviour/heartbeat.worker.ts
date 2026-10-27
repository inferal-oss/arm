// The heartbeat worker (src/behaviour/heartbeat.ts): it hears the page's beats and reports a hang
// once the page answers again. It checks on its own timer, so a hang in progress is known here
// even while the page cannot run anything.
import { HeartbeatWatch, type FromWorker, type ToWorker } from './heartbeat.ts'

const watch = new HeartbeatWatch()
const post = (m: FromWorker) => (self as unknown as { postMessage(m: FromWorker): void }).postMessage(m)

self.onmessage = (e: MessageEvent<ToWorker>) => {
  const m = e.data
  if (m.kind === 'config') watch.hangAfter = m.hangAfter
  else if (m.kind === 'beat') {
    const hang = watch.beat(m.beat)
    if (hang) post({ kind: 'hang', hang })
  }
}

setInterval(() => watch.check(Date.now()), 250)
