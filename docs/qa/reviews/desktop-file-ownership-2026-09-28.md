# Desktop save descriptor ownership review

The full Go run for the local recovery work exposed an intermittent `bad file
descriptor` during temporary-directory cleanup. Review identified a concrete
ownership bug in Unix save-file reuse: `acquireTempAt` wrapped a reusable file in
`os.File`, returned only its integer descriptor, and the caller created a second
wrapper. Garbage collection could close the first wrapper while the second was
writing, or later close a descriptor reused for another file.

The helper now transfers the original `*os.File` to its caller and wraps newly
created raw descriptors exactly once. Existing no-follow checks, inode identity
checks, publication rules, scrubbing and recovery-file retention remain in place.
Review of the surrounding Unix descriptor-return paths found no further instance
of this ownership pattern.

A regression forces garbage collection after acquisition and before writing,
checks both new and existing destinations, and confirms that the published inode
is the reused file. Both cases failed before the fix. Three complete desktop
suite repetitions passed in 3.320 seconds; thirty forced-GC repetitions passed
in 5.525 seconds (`/tmp/apex-desktop-fd-after.log` and
`/tmp/apex-desktop-fd-gc-repeated.log`). This verifies the native save operation;
no dashboard source or user workflow changed. The repository suite is being
rerun separately after the original run finishes.
