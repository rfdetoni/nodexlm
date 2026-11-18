// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package catalog

import (
	"strings"
	"unicode"
)

// IsDecisionModel is a discovery hint, not an assertion that a GGUF implements
// System One. The runtime is authoritative; Hugging Face names are untrusted.
func IsDecisionModel(repository, filename string) bool {
	for _, value := range []string{repository, filename} {
		words := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		for _, word := range words {
			switch word {
			case "decision", "decisions", "decider", "systemone", "system1",
				"winnow", "credence", "jevk5", "jeb", "nimble", "pplxdecider",
				"strandsdecider", "typeddecisions":
				return true
			}
		}
	}
	return false
}
