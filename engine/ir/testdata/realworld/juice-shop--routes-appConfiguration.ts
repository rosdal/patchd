// From https://github.com/juice-shop/juice-shop routes/appConfiguration.ts at 399fba761943.
// Licence: MIT, Copyright (c) 2014-2026 Bjoern Kimminich & the OWASP Juice Shop contributors. See THIRD_PARTY_NOTICES.md.
/*
 * Copyright (c) 2014-2026 Bjoern Kimminich & the OWASP Juice Shop contributors.
 * SPDX-License-Identifier: MIT
 */

import config from 'config'
import { type Request, type Response } from 'express'

export function retrieveAppConfiguration () {
  return (_req: Request, res: Response) => {
    const safeConfig = structuredClone(config.util.toObject(config))
    if (safeConfig.application?.chatBot) {
      delete safeConfig.application.chatBot.llmApiUrl
    }
    res.json({ config: safeConfig })
  }
}
