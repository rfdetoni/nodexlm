// SPDX-License-Identifier: AGPL-3.0-only
// Only the optional relay executor is affected. Never alter affinity of GGML
// compute threads or exceed the cpuset assigned by Android.
#[cfg(any(target_os = "android", test))]
fn lowest_capacity(cores: &[(usize, u64)]) -> Vec<usize> {
    let Some(min) = cores.iter().map(|(_, value)| *value).min() else { return vec![] };
    let max = cores.iter().map(|(_, value)| *value).max().unwrap_or(min);
    if min == 0 || min == max { return vec![]; }
    cores.iter().filter_map(|(cpu, value)| (*value == min).then_some(*cpu)).collect()
}

#[cfg(target_os = "android")]
pub fn prefer_efficient() {
    // CPU capacity is comparable across clusters; do not infer capacity from
    // frequency alone (different microarchitectures need not be comparable).
    unsafe {
        let mut allowed: libc::cpu_set_t = std::mem::zeroed();
        let size = std::mem::size_of::<libc::cpu_set_t>();
        if libc::sched_getaffinity(0, size, &mut allowed) != 0 { return; }
        let mut cores = Vec::new();
        for cpu in 0..libc::CPU_SETSIZE as usize {
            if !libc::CPU_ISSET(cpu, &allowed) { continue; }
            let path = format!("/sys/devices/system/cpu/cpu{cpu}/cpu_capacity");
            let Some(value) = std::fs::read_to_string(path).ok()
                .and_then(|s| s.trim().parse::<u64>().ok()) else { return; };
            cores.push((cpu, value));
        }
        let selected = lowest_capacity(&cores);
        if selected.is_empty() { return; }
        let mut mask: libc::cpu_set_t = std::mem::zeroed();
        for cpu in selected { libc::CPU_SET(cpu, &mut mask); }
        // Best effort. Some OEM kernels forbid this; default scheduling is safe.
        let _ = libc::sched_setaffinity(0, size, &mask);
    }
}
#[cfg(not(target_os = "android"))]
pub fn prefer_efficient() {}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn only_select_measured_heterogeneous_cores() {
        assert_eq!(lowest_capacity(&[(1, 512), (3, 1024), (5, 512)]), vec![1, 5]);
        assert!(lowest_capacity(&[(0, 1024), (1, 1024)]).is_empty());
        assert!(lowest_capacity(&[(0, 0), (1, 1024)]).is_empty());
        assert!(lowest_capacity(&[]).is_empty());
    }
}
