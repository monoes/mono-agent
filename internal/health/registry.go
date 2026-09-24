package health

// Default returns the registry of every built-in check and fix.
func Default() *Registry {
	return NewRegistry(coreChecks(), coreFixes())
}
