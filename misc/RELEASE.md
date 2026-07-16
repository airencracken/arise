# arise Release Runbook

Releasing a new version of arise involves two Git repositories: the main
[arise](https://github.com/airencracken/arise) source repo and the
[arise-overlay](https://github.com/airencracken/arise-overlay) Gentoo ebuild
repo.  The overlay publishes the ebuild that users consume via `emerge`.

---

## 1. Decide the version

Follow [semantic versioning](https://semver.org/).  The current scheme is
`MAJOR.MINOR.PATCH` (no `v` prefix in the tag name, but Git tags include `v`).

Example: `0.2.0`

Export it for the rest of the runbook:

```sh
export V=0.2.0
```

---

## 2. arise repo — prepare the source release

```sh
cd ~/projects/arise
```

### 2.1. Ensure the tree is clean

```sh
git status
```

Untracked files and uncommitted changes must be committed or removed.

### 2.2. Verify module dependencies

```sh
go mod download
go mod verify
make deps VERSION=$V
```

> `make deps` creates `dist/arise-$V-deps.tar.xz`, a locked Go module-cache
> archive. Attach it to the GitHub release so Portage can build without network
> access while the source repository remains unvendored.

### 2.3. Run the full test suite

```sh
make test
make vet
```

Or for a slower, thorough pass:

```sh
make test-v && make vet && make lint
```

If any test fails, fix it before tagging.  A broken tag requires re-tagging
and confuses downstream users.

### 2.4. Build the static binary (sanity check)

```sh
make static
```

This prints `static build: OK` if the binary is statically linked.

### 2.5. Tag and push

```sh
make release VERSION=$V
```

This runs `vendor`, `static`, and `test` (again), then:

```
git tag -a "v$V" -m "arise v$V"
git push origin master --tags
```

If you prefer to do it manually:

```sh
git tag -a "v$V" -m "arise v$V"
git push origin master --tags
```

Verify the tag appears on GitHub:
https://github.com/airencracken/arise/releases

Create the GitHub release and attach the dependency archive before generating
the overlay Manifest:

```sh
gh release create "v$V" "dist/arise-$V-deps.tar.xz" \
  --title "arise v$V" --generate-notes
```

---

## 3. arise-overlay repo — publish the ebuild

```sh
cd ~/projects/arise-overlay
```

### 3.1. Create the versioned ebuild

Copy the live ebuild:

```sh
cp sys-apps/arise/arise-9999.ebuild sys-apps/arise/arise-$V.ebuild
```

The conditional `if [[ ${PV} == 9999 ]]` in the ebuild handles live vs.
versioned behavior automatically — no content changes needed.

### 3.2. Generate the Manifest

```sh
make manifest VERSION=$V
```

This does:

| Step | Action |
|------|--------|
| Download | `curl -sL https://github.com/airencracken/arise/archive/v$V.tar.gz` |
| Checksum | BLAKE2B, SHA512, SHA256, and size for the distfile |
| Checksum | Same algorithms for `arise-$V.ebuild` and `arise-9999.ebuild` |
| Write | Overwrites `sys-apps/arise/Manifest` |

> **If the download fails**, the tag `v$V` hasn't propagated to GitHub yet.
> Wait 30 seconds and retry.  GitHub archive URLs are cached; if you're
> impatient, try `https://codeload.github.com/airencracken/arise/tar.gz/v$V`.

### 3.3. Verify the Manifest

```sh
cat sys-apps/arise/Manifest
```

Should contain four lines: two `DIST` entries (source and dependencies), one
`EBUILD` for the versioned ebuild, and one `EBUILD` for `arise-9999.ebuild`.

### 3.4. Commit and push

```sh
git add sys-apps/arise/
git commit -m "release v$V"
git push origin master
```

---

## 4. Smoke-test the release

### 4.1. On the development machine

```sh
cd /tmp
curl -sLO https://github.com/airencracken/arise/archive/v$V.tar.gz
tar xzf v$V.tar.gz
cd arise-$V
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o arise ./cmd/arise/
./arise info
```

### 4.2. On a Gentoo machine (after overlay push)

```sh
emerge --sync arise-overlay
emerge -av arise
arise info
```

### 4.3. Quick functional smoke test

```sh
arise search gcc
arise search --installed portage
arise info
arise equery list '*/*'
arise env-update
```

---

## 5. Rollback

If a bad release goes out:

1. **arise repo**: delete the tag and re-push:
   ```sh
   git tag -d v$V
   git push origin :refs/tags/v$V
   ```

2. **arise-overlay repo**: revert the commit and re-generate with the correct
   version:
   ```sh
   git revert HEAD
   git push origin master
   ```

---

## Checklist

- [ ] All tests pass (`make test && make vet`)
- [ ] Dependencies verified (`go mod verify`)
- [ ] Static binary builds (`make static` → `static build: OK`)
- [ ] Git tag pushed (`git tag -a v$V -m "arise v$V" && git push --tags`)
- [ ] Versioned ebuild created (`cp arise-9999.ebuild arise-$V.ebuild`)
- [ ] Manifest regenerated (`make manifest VERSION=$V`)
- [ ] Overlay committed and pushed (`git add . && git commit -m "release v$V" && git push`)
- [ ] Smoke-tested on a Gentoo machine (`emerge arise && arise info`)
