// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
#pragma once
#include <algorithm>
#include <cstddef>
#include <vector>

namespace nodexlm {
// Only reuse a complete, previously decoded sequence if the new prompt
// strictly extends it. No rewind or stale final logits.
template <typename Token>
std::size_t appendablePrefix(const std::vector<Token>& decoded,
                             const std::vector<Token>& prompt) {
    if (decoded.empty() || decoded.size() >= prompt.size()) return 0;
    return std::equal(decoded.begin(), decoded.end(), prompt.begin())
        ? decoded.size() : 0;
}
}
