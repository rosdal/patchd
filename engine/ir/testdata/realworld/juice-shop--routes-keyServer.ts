// From https://github.com/juice-shop/juice-shop routes/keyServer.ts at 399fba761943.
// Licence: MIT, Copyright (c) 2014-2026 Bjoern Kimminich & the OWASP Juice Shop contributors. See THIRD_PARTY_NOTICES.md.
/*
 * Copyright (c) 2014-2026 Bjoern Kimminich & the OWASP Juice Shop contributors.
 * SPDX-License-Identifier: MIT
 */

import path from 'node:path'
import { type Request, type Response, type NextFunction } from 'express'

export function serveKeyFiles () {
  return ({ params }: Request, res: Response, next: NextFunction) => {
    const file = params.file

    if (!file.includes('/')) {
      res.sendFile(path.resolve('encryptionkeys/', file))
    } else {
      res.status(403)
      next(new Error('File names cannot contain forward slashes!'))
    }
  }
}
