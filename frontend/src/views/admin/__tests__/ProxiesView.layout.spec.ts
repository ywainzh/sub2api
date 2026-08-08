import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const source = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), '../ProxiesView.vue'), 'utf8')

describe('ProxiesView layout spacing', () => {
  it('keeps the standard proxy toolbar separated from the view tabs', () => {
    expect(source).toContain('<TablePageLayout v-else class="pt-4">')
  })
})
