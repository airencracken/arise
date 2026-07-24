# Portage self-hosting milestone — 2026-07-24

## Result

Arise successfully built, installed, committed, and finalized
`sys-apps/portage-3.0.81.2` on the live Gentoo system during a deep `@world`
update.

This is a self-hosting interoperability milestone: Arise can execute the
ebuild and lifecycle needed to replace the reference package manager while
maintaining ordinary Gentoo VDB, filesystem, configuration-protection,
environment, logging, and package-message conventions.

The package completed as action 8 of 25 in continuation v13. The later v15
continuation committed the final seven actions successfully, including Firefox
140.12.0. Post-run verification then exposed malformed empty-package
`CONTENTS` records and missed stale built-dependency rebuilds. Those defects
were repaired, but a later repository sync introduced a separate 19-action
update that remains subject to execution and verification.

This is therefore evidence for the Portage-package gate specifically, not a
claim that the complete world-update or bootstrap gates have passed.

## What this establishes

- Arise can install the reference package manager as a normal Gentoo package.
- The result remains visible to ordinary Gentoo package-state and log tooling.
- Arise's execution model handles a package that exercises a dense collection
  of Python, filesystem-layout, lifecycle, environment-regeneration, protected
  configuration, and VDB behavior.
- Portage remains the compatibility reference and an important adversarial
  workload even when Arise is the executing package manager.

## System-construction acceptance ladder

Each gate is cumulative. A later gate does not replace the evidence required
by an earlier one.

### G0 — Portage package self-hosting

- [x] Build and merge `sys-apps/portage-3.0.81.2` with Arise.
- [x] Commit and finalize the package transaction successfully.
- [ ] Add a reproducible isolated-root fixture for this package-level gate.

### G1 — Existing stage3 maintenance

- [ ] Start from an unmodified, documented Gentoo stage3.
- [ ] Use Arise to perform a complete deep/newuse world update.
- [ ] Execute all package builds and merges through Arise, not Portage.
- [ ] Finish with no active journals, preserved-library consumers, unresolved
  dependency conflicts, or VDB ownership inconsistencies.
- [ ] Reboot and validate the resulting system.

Passing G1 establishes that Arise can maintain Gentoo.

### G2 — Stage3 empty-tree closure

- [ ] Starting from the validated G1 system, complete an `--emptytree` rebuild.
- [ ] Verify the rebuilt dependency closure against the frozen approved plan.
- [ ] Validate toolchain, dynamic linker, Python, Perl, Rust, LLVM, desktop,
  configuration-protection, and preserved-library health.
- [ ] Reboot and repeat the post-boot validation.

### G3 — Stage1/bootstrap to stage3-equivalent system

- [ ] Define the exact bootstrap input, profile, repository snapshot, world
  set, compiler/bootstrap binaries, and permitted host-tool boundary.
- [ ] Construct a stage3-equivalent root from that minimal environment with
  Arise.
- [ ] Prove that no undeclared host dependency contaminated the result.
- [ ] Chroot or boot into the result and complete its world update with Arise.
- [ ] Compare package closure, VDB metadata, filesystem ownership, profile
  state, and essential command behavior with the corresponding Gentoo stage3.

Passing G3 establishes that Arise can construct Gentoo, not only maintain it.

### G4 — Independent bootstrap and reproducibility

- [ ] Repeat G3 from a clean snapshot with recorded inputs and no state reused
  from the first construction.
- [ ] Produce inspectable manifests for inputs, plans, binary artifacts, VDB
  state, configuration decisions, journals, and validation output.
- [ ] Explain all non-reproducible artifacts and compare normalized system
  outputs.
- [ ] Complete an Arise-driven empty-tree rebuild and reboot of the independently
  constructed system.

Passing G4 establishes a repeatable independent Gentoo bootstrap
implementation.

## Required validation at every root-level gate

- Dependency closure and selected USE/profile state match the approved plan.
- VDB records, counters, owners, slots/subslots, repositories, and phase ABI
  metadata are internally consistent.
- No transaction journal remains active or ambiguously committed.
- Preserved-library and dynamic-linker scans are clean.
- Native C and C++ compilation and linking work with the selected toolchain.
- Installed Python, Perl, Rust, LLVM, package-manager, and essential system
  commands pass representative runtime probes.
- Protected configuration updates are enumerated and reviewed deliberately.
- The system boots and reaches its intended service/login target.
- Common Gentoo inspection tools can consume Arise's compatibility logs and
  package state.
- Evidence records the repository commit, profile, configuration, stage
  source, Arise binary hash, plan hash, and machine/environment details.

## Naming principle

- G1: Arise can **maintain Gentoo**.
- G3: Arise can **construct Gentoo**.
- G4: Arise is an **independent, repeatable Gentoo bootstrap
  implementation**.
