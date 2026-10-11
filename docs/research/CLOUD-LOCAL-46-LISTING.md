# #46: local-folder listing error — partial fix only

- Upstream issue: https://github.com/Liquid-co/OpenSave/issues/46
- Source research SHA: `e4c113ff3af3b9556444c1e511e15531f3b529c2`
- Pinned base `dev-public`: `7b690420c0fa03513764f4937af29bebf7f391a7`
- Branch is **fork-only**. It is not an accepted upstream implementation.

## Change

Return the actual `os.ReadDir` error from `localFolder.list()` instead of converting it to an empty successful listing. Tests cover absent, non-directory, and normally readable destinations.

## Remaining risk

`localFolder.put()` intentionally uses `writeFileWhole()`, which creates an absent destination path. An unmounted NAS can therefore still become a writable local fallback, even though the listing error is now surfaced when the configured directory is absent. A writable mountpoint underlying an unmounted share cannot be distinguished from the intended filesystem without a separate opt-in mount-identity contract. This is *not* a complete resolution of #46.
