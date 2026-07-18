# Errcheck Warnings (27 Total)

> Historical development audit; retain for context until superseded or pruned.

## internal/binpkg/binpkg.go (9 warnings)

| Line | Call | Risk |
|------|------|------|
| 320 | `os.Remove(target)` | Temp file leak on error |
| 372 | `bzWriter.Close()` | bzip2 process leak |
| 373 | `tmpF.Close()` | File handle leak |
| 383 | `bzWriter.Close()` | bzip2 process leak |
| 384 | `tmpF.Close()` | File handle leak |
| 391 | `bzWriter.Close()` | bzip2 process leak |
| 392 | `tmpF.Close()` | File handle leak |
| 428 | `srcF.Close()` | File handle leak |
| 434 | `srcF.Close()` | File handle leak |

**Context**: `Create()` function builds binary packages. Multiple cleanup paths on error.

---

## internal/features/features.go (4 warnings)

| Line | Call | Risk |
|------|------|------|
| 181 | `outF.Close()` | File handle leak (split-log stdout) |
| 184 | `errF.Close()` | File handle leak (split-log stderr) |
| 197 | `wc.Close()` | WaitGroup/cmd leak |
| 200 | `wc.Close()` | WaitGroup/cmd leak |

**Context**: `ApplyToEnv()` sets up split-log feature with tee processes.

---

## internal/fetch/fetch.go (5 warnings)

| Line | Call | Risk |
|------|------|------|
| 133 | `resp.Body.Close()` | HTTP connection leak |
| 146 | `fh.Close()` | File handle leak |
| 147 | `os.Remove(tmpPath)` | Temp file leak on error |
| 150 | `fh.Close()` | File handle leak |
| 153 | `os.Remove(tmpPath)` | Temp file leak on error |

**Context**: `Fetch()` downloads distfiles with resume support.

---

## internal/merge/merge.go (3 warnings)

| Line | Call | Risk |
|------|------|------|
| 253 | `in.Close()` | File handle leak (source file) |
| 259 | `out.Close()` | File handle leak (dest file) |
| 284 | `dh.Close()` | Directory handle leak |

**Context**: `Merge()` copies files to root filesystem.

---

## internal/news/news.go (1 warning)

| Line | Call | Risk |
|------|------|------|
| 246 | `f.Close()` | File handle leak |

**Context**: Reading news item files.

---

## internal/phase/phase_test.go (1 warning)

| Line | Call | Risk |
|------|------|------|
| 269 | `tw.Close()` | Tar writer leak (test only) |

---

## internal/features/features_test.go (2 warnings)

| Line | Call | Risk |
|------|------|------|
| 153 | `os.Setenv()` | Env var leak (test only) |
| 154 | `os.Unsetenv()` | Env var leak (test only) |

---

## internal/fetch/fetch_test.go (3 warnings)

| Line | Call | Risk |
|------|------|------|
| 72 | `w.Write()` | Bytes lost (test only) |
| 100 | `w.Write()` | Bytes lost (test only) |
| 134 | `w.Write()` | Bytes lost (test only) |

---

## internal/merge/collision_test.go (4 warnings)

| Line | Call | Risk |
|------|------|------|
| 191 | `os.MkdirAll()` | Dir creation failure ignored (test only) |
| 192 | `os.WriteFile()` | Write failure ignored (test only) |
| 195 | `os.MkdirAll()` | Dir creation failure ignored (test only) |
| 196 | `os.WriteFile()` | Write failure ignored (test only) |
| 248 | `os.MkdirAll()` | Dir creation failure ignored (test only) |
| 250 | `os.WriteFile()` | Write failure ignored (test only) |

---

## Recommendation

Add proper error handling for production code (binpkg, features, fetch, merge, news):
```go
// Instead of:
defer f.Close()

// Use:
defer func() { _ = f.Close() }()

// Or with error capture:
if err := f.Close(); err != nil {
    log.Printf("close failed: %v", err)
}
```

For test code, add `_ =` to explicitly ignore (or fix to check).
