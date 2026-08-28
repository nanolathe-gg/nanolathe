# Probes

Probes are small, data-driven scenarios for checking Nanolathe against the
retail content contract. They load authored maps, missions, and definitions
through the same VFS and catalog entry points used by the single-player
runtime; they do not contain copied retail bytes or alternate simulation
rules.

Keep scenarios under this directory in the format they exercise (for example,
`*.ota` for map setup). A probe should state the behavior it observes and cite
the owning research section. Prefer a focused scenario that records a stable
relationship, ordering, or hash over a census or implementation milestone.
