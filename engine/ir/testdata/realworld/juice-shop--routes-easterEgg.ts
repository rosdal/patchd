// From https://github.com/juice-shop/juice-shop routes/easterEgg.ts at 399fba761943.
// Licence: MIT, Copyright (c) 2014-2026 Bjoern Kimminich & the OWASP Juice Shop contributors. See THIRD_PARTY_NOTICES.md.
/*
 * Copyright (c) 2014-2026 Bjoern Kimminich & the OWASP Juice Shop contributors.
 * SPDX-License-Identifier: MIT
 */

import path from 'node:path'
import { type Request, type Response } from 'express'
import { challenges } from '../data/datacache'
import * as challengeUtils from '../lib/challengeUtils'

export function serveEasterEgg () {
  return (req: Request, res: Response) => {
    challengeUtils.solveIf(challenges.easterEggLevelTwoChallenge, () => { return true })
    res.sendFile(path.resolve('frontend/dist/frontend/assets/private/threejs-demo.html'))
  }
}
