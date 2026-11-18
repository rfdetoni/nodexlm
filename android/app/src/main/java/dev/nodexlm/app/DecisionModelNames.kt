// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

/** Discovery hint only. The native runtime determines actual System One compatibility. */
internal object DecisionModelNames {
    private val names = setOf(
        "decision", "decisions", "decider", "systemone", "system1",
        "winnow", "credence", "jevk5", "jeb", "nimble", "pplxdecider",
        "strandsdecider", "typeddecisions"
    )

    fun likely(repository: String, filename: String): Boolean =
        sequenceOf(repository, filename).any { value ->
            value.lowercase(java.util.Locale.ROOT)
                .split(Regex("[^\\p{L}\\p{N}]+"))
                .any { it in names }
        }
}
