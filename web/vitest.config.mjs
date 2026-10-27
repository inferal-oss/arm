import {defineConfig} from 'vitest/config';
import {resolve} from 'node:path';
export default defineConfig({
 root:import.meta.dirname,
 resolve:{alias:{'@inferal-oss/semblance':resolve(import.meta.dirname,'third_party/semblance/src/index.ts')}},
 test:{include:['test/**/*.test.mjs'],testTimeout:60000},
});
