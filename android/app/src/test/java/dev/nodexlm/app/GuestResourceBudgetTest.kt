// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class GuestResourceBudgetTest {
    private val mib = 1024L * 1024L

    @Test fun autoKeepsPlatformHeadroom() {
        assertEquals(512 * mib, GuestResourceBudget.reserveBytes(0, 4096 * mib, 128 * mib))
        assertEquals(956 * mib, GuestResourceBudget.reserveBytes(0, 4096 * mib, 700 * mib))
        assertEquals(1024 * mib, GuestResourceBudget.reserveBytes(0, 16384 * mib, 128 * mib))
    }

    @Test fun explicitReserveAndSystemFloorArePreserved() {
        assertEquals(2048 * mib, GuestResourceBudget.reserveBytes(2048, 8192 * mib, 128 * mib))
        assertEquals(1256 * mib, GuestResourceBudget.reserveBytes(512, 8192 * mib, 1000 * mib))
        assertEquals(512 * mib, GuestResourceBudget.reserveBytes(-1, 4096 * mib, 0))
    }

    @Test fun startsSmallAndGrowsWithoutReusingSlots() {
        val pool = RpcTunnelBudget()
        assertEquals(listOf(0, 1), pool.grow(0).toList())
        assertEquals(listOf(2), pool.grow(1).toList())
        assertTrue(pool.grow(0).isEmpty())
        assertEquals((3..7).toList(), pool.grow(99).toList())
        assertTrue(pool.grow(8).isEmpty())
    }

    @Test fun concurrentClaimsDoNotDuplicateSlots() {
        val pool = RpcTunnelBudget()
        val slots = java.util.Collections.synchronizedList(mutableListOf<Int>())
        val threads = (0..8).map { n ->
            kotlin.concurrent.thread { slots.addAll(pool.grow(n.toLong()).toList()) }
        }
        threads.forEach { it.join() }
        assertEquals((0..7).toList(), slots.sorted())
    }
}
