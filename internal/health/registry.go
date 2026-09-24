package health

// Default returns the registry of every built-in check and fix.
func Default() *Registry {
	var checks []Check
	checks = append(checks, coreChecks()...)
	checks = append(checks, monomindChecks()...)
	checks = append(checks, monomindDoctorChecks()...)
	checks = append(checks, runtimeChecks()...)
	checks = append(checks, browserChecks()...)
	checks = append(checks, serviceChecks()...)
	checks = append(checks, integrationChecks()...)
	checks = append(checks, accountChecks()...)
	var fixes []Fix
	fixes = append(fixes, coreFixes()...)
	fixes = append(fixes, monomindFixes()...)
	fixes = append(fixes, monomindDoctorFixes()...)
	fixes = append(fixes, runtimeFixes()...)
	fixes = append(fixes, browserFixes()...)
	fixes = append(fixes, serviceFixes()...)
	fixes = append(fixes, integrationFixes()...)
	fixes = append(fixes, accountFixes()...)
	return NewRegistry(checks, fixes)
}
