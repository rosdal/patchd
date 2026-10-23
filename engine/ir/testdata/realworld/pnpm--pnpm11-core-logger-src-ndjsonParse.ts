// From https://github.com/pnpm/pnpm pnpm11/core/logger/src/ndjsonParse.ts at cbe5accc7ace.
// Licence: MIT, Copyright (c) 2015-2016 Rico Sta. Cruz and other contributors. See THIRD_PARTY_NOTICES.md.
// From https://www.npmjs.com/package/ndjson, but with updated deps, only parse and hardcoded options

import type { Transform } from 'node:stream'

import split from 'split2'

const opts = { strict: true }

export function parse (): Transform {
  function parseRow (this: Transform, row: string) {
    try {
      if (row) return JSON.parse(row)
    } catch (_e) {
      if (opts.strict) {
        this.emit('error', new Error(`Could not parse row "${row.length > 50 ? `${row.slice(0, 50)}...` : row}"`))
      }
    }
  }

  return split(parseRow, opts)
}
