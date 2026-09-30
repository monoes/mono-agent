package main

// ─────────────────────────────────────────────────────────────────────────────
// Signed org definitions (#288, monomind#502). monomind 2.21 runs an org
// only when the operator signed its definition. The org view and designer
// show a banner for an unsigned or changed org; "Review & sign" shows
// monomind's own review and signs only after the user confirms, and only
// the file that was reviewed (its sha256). All of it is `monoagentcli org
// sign`; nothing here signs or verifies by itself.
// ─────────────────────────────────────────────────────────────────────────────

// orgSignArgs builds `org [--project <root>] sign <org> [extra...]`.
func orgSignArgs(root, orgName string, extra ...string) []string {
	args := []string{"org"}
	if root != "" {
		args = append(args, "--project", root)
	}
	return append(append(args, "sign", orgName), extra...)
}

// OrgSignatureStatus is `org sign <org> --status`: {supported, state,
// detail, sha256, message}. supported is false below monomind 2.21.
func (a *App) OrgSignatureStatus(orgName string) string {
	return a.jsonResult(orgSignArgs(a.orgProjectRoot(), orgName, "--status")...)
}

// OrgSignatureReview is `org sign <org>` without --yes: monomind's review
// text and the sha256 of the file it reviewed. It signs nothing.
func (a *App) OrgSignatureReview(orgName string) string {
	return a.jsonResult(orgSignArgs(a.orgProjectRoot(), orgName)...)
}

// OrgSign signs the reviewed definition. The frontend calls it only from
// the review dialog's confirm button; sha256 is the review's, so a file
// that changed since is refused rather than signed.
func (a *App) OrgSign(orgName, sha256 string) string {
	return a.jsonResult(orgSignArgs(a.orgProjectRoot(), orgName, "--yes", "--expect-sha256", sha256)...)
}
