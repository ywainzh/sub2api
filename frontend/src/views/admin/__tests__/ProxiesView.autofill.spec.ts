import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const currentDir = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(resolve(currentDir, '../ProxiesView.vue'), 'utf8')

const formMarkup = (id: string) => {
  const idIndex = source.indexOf(`id="${id}"`)
  const start = source.lastIndexOf('<form', idIndex)
  const end = source.indexOf('</form>', idIndex)
  return source.slice(start, end)
}

const inputMarkup = (name: string) => {
  const nameIndex = source.indexOf(`name="${name}"`)
  const start = source.lastIndexOf('<input', nameIndex)
  const end = source.indexOf('/>', nameIndex)
  return source.slice(start, end)
}

describe('admin proxy credential autofill protection', () => {
  it.each(['create-proxy-form', 'edit-proxy-form'])('disables form-level autofill for %s', (id) => {
    const markup = formMarkup(id)

    expect(markup).toContain('autocomplete="off"')
    expect(markup).toContain('data-form-type="other"')
  })

  it.each(['proxy-create-principal', 'proxy-edit-principal'])(
    'protects proxy username field %s from login autofill',
    (name) => {
      const markup = inputMarkup(name)

      expect(markup).toContain('autocomplete="off"')
      expect(markup).toContain('readonly')
      expect(markup).toContain('data-1p-ignore')
      expect(markup).toContain('data-lpignore="true"')
      expect(markup).toContain('data-bwignore="true"')
      expect(markup).toContain('@pointerdown="unlockProxyCredentialField"')
      expect(markup).toContain('@keydown="unlockProxyCredentialField"')
      expect(markup).toContain('@paste="unlockProxyCredentialField"')
    }
  )

  it.each(['proxy-create-secret', 'proxy-edit-secret'])(
    'protects proxy password field %s from saved-password autofill',
    (name) => {
      const markup = inputMarkup(name)

      expect(markup).toContain('autocomplete="new-password"')
      expect(markup).toContain('readonly')
      expect(markup).toContain('data-1p-ignore')
      expect(markup).toContain('data-lpignore="true"')
      expect(markup).toContain('data-bwignore="true"')
      expect(markup).toContain('@pointerdown="unlockProxyCredentialField"')
      expect(markup).toContain('@keydown="unlockProxyCredentialField"')
      expect(markup).toContain('@paste="unlockProxyCredentialField"')
    }
  )

  it('only unlocks credential inputs after explicit user interaction', () => {
    expect(source).toContain('const unlockProxyCredentialField = (event: Event) => {')
    expect(source).toContain('input.readOnly = false')
  })
})
