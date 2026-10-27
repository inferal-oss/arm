import { mkdirSync } from 'node:fs'
import { execFileSync } from 'node:child_process'

const source = 'node_modules/@inferal-oss/semblance/rdf-engine/'
mkdirSync(source + 'build', { recursive: true })
execFileSync(process.execPath, ['--import', 'tsx', source + 'wat/build.ts', source + 'build/wat.wasm'], { stdio: 'inherit' })
