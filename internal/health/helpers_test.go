package health

// fixesList returns the registered fixes (tests build partial registries
// from them).
func (r *Registry) fixesList() []Fix {
	out := make([]Fix, 0, len(r.fixes))
	for _, f := range r.fixes {
		out = append(out, f)
	}
	return out
}
