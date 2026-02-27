const VERSION_PREFIX_REGEX = /^v/i
const VERSION_SPLIT_REGEX = /\./
const VERSION_PART_RADIX = 10
const VERSION_PART_DEFAULT = 0
const COMPARE_LESS = -1
const COMPARE_EQUAL = 0
const COMPARE_GREATER = 1

const normalizeVersion = (value: string): string => value.replace(VERSION_PREFIX_REGEX, '').trim()

export const compareVersions = (current: string, remote: string): number => {
  const currentParts = normalizeVersion(current)
    .split(VERSION_SPLIT_REGEX)
    .map((part) => parseInt(part, VERSION_PART_RADIX) || VERSION_PART_DEFAULT)
  const remoteParts = normalizeVersion(remote)
    .split(VERSION_SPLIT_REGEX)
    .map((part) => parseInt(part, VERSION_PART_RADIX) || VERSION_PART_DEFAULT)

  const maxLen = Math.max(currentParts.length, remoteParts.length)
  for (let i = 0; i < maxLen; i++) {
    const cur = currentParts[i] ?? VERSION_PART_DEFAULT
    const rem = remoteParts[i] ?? VERSION_PART_DEFAULT
    if (cur === rem) continue
    return cur < rem ? COMPARE_LESS : COMPARE_GREATER
  }
  return COMPARE_EQUAL
}
