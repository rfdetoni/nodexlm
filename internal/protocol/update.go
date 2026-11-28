package protocol

// UpdateDirective contains only an expected release identity. It cannot carry
// executable commands, paths, URLs or credentials.
type UpdateDirective struct {
	RequiredBuildCommit string               `json:"required_build_commit,omitempty"`
	RequiredVersion     string               `json:"required_version,omitempty"`
	UpdateRequired      bool                 `json:"update_required"`
	Runtime             *SignedRuntimePolicy `json:"runtime_policy,omitempty"`
}

func ValidBuildCommit(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
