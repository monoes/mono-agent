import { useEffect, useRef, useState } from 'react'
import { api } from '../../services/api.js'

// `org validate`'s report for the designer (#205): monomind's own checks,
// including full-access taint problems (a full-access role that reads
// untrusted input, or is reachable from one via reports_to). Its "error" is
// one problem per line; the designer shows them next to its structural
// checks.

// reportProblems splits a report into { errors, warnings }.
export function reportProblems(report) {
  if (!report || report.valid !== false) return { errors: [], warnings: report?.warnings || [] }
  const errors = String(report.error || 'invalid org config')
    .split('\n').map(l => l.trim().replace(/^[-•]\s*/, '')).filter(Boolean)
  return { errors, warnings: report.warnings || [] }
}

// useOrgValidateReport runs `org validate` for orgName, again whenever stamp
// changes (debounced). A failed call contributes nothing: the structural
// checks still stand, and a save is validated by the CLI regardless.
export function useOrgValidateReport(orgName, stamp = '') {
  const [problems, setProblems] = useState({ errors: [], warnings: [] })
  const current = useRef(orgName)
  current.current = orgName
  useEffect(() => {
    if (!orgName) { setProblems({ errors: [], warnings: [] }); return }
    const name = orgName
    const id = setTimeout(() => {
      Promise.resolve().then(() => api.validateOrgReport(name))
        .then(report => { if (current.current === name) setProblems(reportProblems(report)) })
        .catch(() => { if (current.current === name) setProblems({ errors: [], warnings: [] }) })
    }, stamp ? 600 : 0)
    return () => clearTimeout(id)
  }, [orgName, stamp])
  return problems
}

// mergeValidation adds the CLI's errors (not already reported by the
// structural checks) to validateStructure's { valid, errors }.
export function mergeValidation(structural, cli) {
  const extra = (cli?.errors || []).filter(e => !structural.errors.includes(e))
  return {
    ...structural,
    valid: structural.valid && extra.length === 0,
    errors: [...structural.errors, ...extra],
    cliErrors: extra,
    warnings: cli?.warnings || [],
  }
}
