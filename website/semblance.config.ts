import { readFileSync } from 'node:fs'
import { defineSite } from '@inferal-oss/semblance/node'

export default defineSite({
  root: 'https://getarm.org/ui#shell',
  store: 'wat',
  graphs: () => [readFileSync(new URL('./app.ttl', import.meta.url), 'utf8')],
  prefer: 'sparql',
})
