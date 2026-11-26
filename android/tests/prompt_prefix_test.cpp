// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
#include "../app/src/main/cpp/prompt_prefix.h"
#include <cassert>
#include <vector>

int main() {
    using V = std::vector<int>;
    using nodexlm::appendablePrefix;
    assert(appendablePrefix(V{}, V{1}) == 0);
    assert(appendablePrefix(V{1, 2}, V{1, 2, 3}) == 2);
    assert(appendablePrefix(V{1, 2}, V{1, 2}) == 0);
    assert(appendablePrefix(V{1, 2}, V{1}) == 0);
    assert(appendablePrefix(V{1, 2}, V{1, 3, 4}) == 0);
    assert(appendablePrefix(V{1, 2}, V{4, 1, 2}) == 0);
    assert(appendablePrefix(V{1, 2}, V{}) == 0);
}
