# 0009. `apply` installs exactly the plan

Status: Accepted · 2026-10-14

## Context

Between the moment Patchd decides what to install and the moment dpkg runs, the package lists can change: a refresh, a mirror sync, a repository someone added. If `apply` simply runs `apt-get dist-upgrade`, what gets installed is whatever apt resolves at that second, which may not be what `patchd plan` showed, what the security filter selected, or what the run log claims. For an unattended tool that is the difference between "I know what changed last night" and "something changed last night".

## Decision

A plan is a list of exact versions: every update is pinned to the version shown at plan time. `apply` builds a fresh plan, then:

1. **Simulates** the exact install command. Any removal, or any package install or upgrade that is not in the plan (other than a new dependency of a planned package), aborts the run before anything is downloaded.
2. **Downloads** everything with the same command and `--download-only`. A network or repository failure stops the run while dpkg is still untouched.
3. **Installs** with the same command, then verifies that every planned package is at its planned version.

The command is pinned wherever it can be: `apt-get install --only-upgrade pkg=version …` for the security filter, and whenever the plan excludes something. The `all` filter with no exclusions runs `apt-get dist-upgrade`, which apt needs for new kernels and Proxmox, and relies on step 1 to prove that the result equals the plan. dnf gets the same three passes with NEVRAs.

Held packages are never in a plan. They are listed as skipped, so the report still shows them. Removals are refused unless `allow_removals = true`.

## Consequences

- `patchd plan` is an honest dry run: it is the same plan `apply` builds.
- The run log says precisely what changed, version by version.
- A plan is serializable, so v0.7's signed jobs can be exactly this document.
- Runs abort more often than a plain `dist-upgrade` would, for example when a security update needs an upgraded dependency that the security filter did not select. That is deliberate: the abort is logged with the reason, and the next run (or a change of filter) picks it up.
- Naming a package in `apt-get install` marks it as manually installed. `apply` records which planned packages were automatic and restores that mark afterwards.

## Alternatives considered

- **Run `apt-get dist-upgrade` / `dnf upgrade` and report what happened.** Simplest, and what `unattended-upgrades` effectively does, but there is no plan to compare against, so nothing can prove the run did what was intended.
- **Apply a plan, but tolerate extra packages.** Hides exactly the case this decision is about.
- **`apt-mark hold` excluded packages for the duration of the run.** Would make `dist-upgrade` honour exclusions, but changes system state that a crash could leave behind.
