// Package hostread is omnistat's one source of host readings (ADR-0008,
// ADR-0009) and the only package in the repository that imports gopsutil.
//
// It exposes one small reader type per area (CPU, Memory). Each returns what
// the operating system reports, as plain structs; what a reading *means* —
// which CPU states are idle, how memory is rounded, what is published — is the
// consuming module's decision, made behind a Reader interface that module
// declares and owns, so its tests never touch this package.
//
// Every exported method obeys ADR-0008's two rules:
//
//   - Stateless reads only. No package-level baseline, no cache of a previous
//     reading, no goroutine that outlives the call (ADR-0005). State that a
//     rate needs belongs in the provider (ADR-0006). gopsutil's cpu.Percent and
//     its Windows load.Avg emulation are therefore never called.
//   - Only readings the operating system maintains. Where gopsutil emulates a
//     value on some platform, the doc comment of the method that returns it
//     says so and names the attribute gating (ADR-0007) that keeps it from
//     being published there.
package hostread
