// From https://github.com/juice-shop/juice-shop lib/is-docker.ts at 399fba761943.
// Licence: MIT, Copyright (c) 2014-2026 Bjoern Kimminich & the OWASP Juice Shop contributors. See THIRD_PARTY_NOTICES.md.
// from https://github.com/sindresorhus/is-docker/tree/main MIT Licensed
// inlined to avoid import problems in cypress

import fs from 'node:fs'

let isDockerCached: boolean | undefined

function hasDockerEnv () {
  try {
    fs.statSync('/.dockerenv')
    return true
  } catch {
    return false
  }
}

function hasDockerCGroup () {
  try {
    return fs.readFileSync('/proc/self/cgroup', 'utf8').includes('docker')
  } catch {
    return false
  }
}

function hasDockerMountInfo () {
  try {
    return fs.readFileSync('/proc/self/mountinfo', 'utf8').includes('/docker/containers/')
  } catch {
    return false
  }
}

export default function isDocker () {
  isDockerCached ??= hasDockerEnv() || hasDockerCGroup() || hasDockerMountInfo()
  return isDockerCached
}
