package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/airencracken/arise/internal/atom"
	"github.com/airencracken/arise/internal/metadata"
	"github.com/dgraph-io/badger/v4"
)

const (
	keyPrefix          = "pkg:"
	fingerprintPrefix  = "fp:"
	cpIndexPrefix      = "idx:cp:"
	slotIndexPrefix    = "idx:slot:"
	repoIndexPrefix    = "idx:repo:"
	keywordIndexPrefix = "idx:keyword:"
	licenseIndexPrefix = "idx:license:"
	eapiIndexPrefix    = "idx:eapi:"
	schemaKey          = "meta:schema"
	writerKey          = "meta:writer"
	SchemaVersion      = 3
	maxBatchSize       = 250
)

// WriterVersion is set by the CLI before opening a writable database.
var WriterVersion = "unknown"

type ReconcileStats struct {
	Seen      int
	Changed   int
	Unchanged int
	Removed   int
}

// OpenDB opens or creates a BadgerDB database at the given path.
// Callers are responsible for closing the returned DB via db.Close().
func OpenDB(path string) (*badger.DB, error) {
	opts := badger.DefaultOptions(path).
		WithValueLogFileSize(1 << 30).
		WithValueThreshold(1 << 20).
		WithLoggingLevel(badger.WARNING)

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("ingest: open db: %w", err)
	}
	if err := ensureSchema(db, true); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// OpenReadOnlyDB opens an existing index without creating files or taking a
// writer lock. Read-only commands must use this path so an index maintained by
// root remains usable by ordinary users.
func OpenReadOnlyDB(path string) (*badger.DB, error) {
	valueDir, err := prepareReadOnlyValueDir(path)
	if err != nil {
		return nil, fmt.Errorf("ingest: prepare private read-only value-log view: %w", err)
	}
	opts := badger.DefaultOptions(path).
		WithReadOnly(true).
		WithValueDir(valueDir).
		WithLogger(nil)
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("ingest: open read-only db: %w", err)
	}
	if err := ensureSchema(db, false); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// Badger v4 opens value logs and DISCARD O_RDWR even in read-only mode. Keep
// the authoritative system index immutable and give each user a small private
// value-log view; SSTables remain shared directly from the published snapshot.
func prepareReadOnlyValueDir(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	identity := sha256.Sum256([]byte(resolved + "\x00" + strconv.FormatInt(info.ModTime().UnixNano(), 10)))
	cacheBase, err := os.UserCacheDir()
	if err != nil || cacheBase == "" {
		cacheBase = os.TempDir()
	}
	root := filepath.Join(cacheBase, "arise", "badger-readonly")
	if err := os.MkdirAll(root, 0700); err != nil {
		root = filepath.Join(os.TempDir(), "arise-badger-readonly-"+strconv.Itoa(os.Geteuid()))
		if fallbackErr := os.MkdirAll(root, 0700); fallbackErr != nil {
			return "", fmt.Errorf("cache directory: %v; temporary fallback: %w", err, fallbackErr)
		}
	}
	target := filepath.Join(root, fmt.Sprintf("%x", identity[:12]))
	if _, err := os.Stat(filepath.Join(target, ".complete")); err == nil {
		return target, nil
	}
	temporary, err := os.MkdirTemp(root, ".build-")
	if err != nil {
		root = filepath.Join(os.TempDir(), "arise-badger-readonly-"+strconv.Itoa(os.Geteuid()))
		if fallbackErr := os.MkdirAll(root, 0700); fallbackErr != nil {
			return "", fmt.Errorf("user cache: %v; temporary fallback: %w", err, fallbackErr)
		}
		target = filepath.Join(root, fmt.Sprintf("%x", identity[:12]))
		if _, statErr := os.Stat(filepath.Join(target, ".complete")); statErr == nil {
			return target, nil
		}
		temporary, err = os.MkdirTemp(root, ".build-")
		if err != nil {
			return "", err
		}
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(temporary)
		}
	}()
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (name != "DISCARD" && filepath.Ext(name) != ".vlog") {
			continue
		}
		if err := copyFile(filepath.Join(resolved, name), filepath.Join(temporary, name)); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(temporary, ".complete"), nil, 0600); err != nil {
		return "", err
	}
	if err := os.Rename(temporary, target); err != nil {
		if _, statErr := os.Stat(filepath.Join(target, ".complete")); statErr != nil {
			return "", err
		}
	} else {
		cleanup = false
	}
	return target, nil
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func ensureSchema(db *badger.DB, writable bool) error {
	var version int
	err := db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(schemaKey))
		if err == badger.ErrKeyNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		return item.Value(func(value []byte) error {
			parsed, err := strconv.Atoi(string(value))
			if err != nil {
				return fmt.Errorf("invalid schema version %q", value)
			}
			version = parsed
			return nil
		})
	})
	if err != nil {
		return fmt.Errorf("ingest: read schema: %w", err)
	}
	if version > SchemaVersion {
		return fmt.Errorf("ingest: database schema %d is newer than supported schema %d; upgrade Arise", version, SchemaVersion)
	}
	if writable && version < SchemaVersion {
		if err := ResetPackageIndex(db); err != nil {
			return fmt.Errorf("ingest: migrate schema %d to %d: %w", version, SchemaVersion, err)
		}
		if err := db.Update(func(txn *badger.Txn) error { return txn.Set([]byte(schemaKey), []byte(strconv.Itoa(SchemaVersion))) }); err != nil {
			return fmt.Errorf("ingest: initialize schema: %w", err)
		}
	}
	if writable {
		if err := db.Update(func(txn *badger.Txn) error {
			return txn.Set([]byte(writerKey), []byte(WriterVersion))
		}); err != nil {
			return fmt.Errorf("ingest: record writer version: %w", err)
		}
	}
	return nil
}

// MakeReadable publishes repository-derived index data for unprivileged query
// commands. The index contains no privileged package-manager state.
func MakeReadable(path string) error {
	return filepath.Walk(path, func(current string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if info.IsDir() {
			mode = 0755
		}
		if err := os.Chmod(current, mode); err != nil {
			return fmt.Errorf("publish %s: %w", current, err)
		}
		return nil
	})
}

// ResetPackageIndex removes all indexed package metadata while preserving any
// future non-package keys stored in the same database.
func ResetPackageIndex(db *badger.DB) error {
	if err := db.DropPrefix([]byte(keyPrefix)); err != nil {
		return fmt.Errorf("ingest: reset package index: %w", err)
	}
	if err := db.DropPrefix([]byte(fingerprintPrefix)); err != nil {
		return fmt.Errorf("ingest: reset fingerprint index: %w", err)
	}
	for _, prefix := range []string{cpIndexPrefix, slotIndexPrefix, repoIndexPrefix, keywordIndexPrefix, licenseIndexPrefix, eapiIndexPrefix} {
		if err := db.DropPrefix([]byte(prefix)); err != nil {
			return fmt.Errorf("ingest: reset secondary index %s: %w", prefix, err)
		}
	}
	return nil
}

// Ingest reads PackageMetadata entries from the channel and writes them into
// the BadgerDB using batched writes. It returns the total number of entries
// ingested.
func Ingest(db *badger.DB, entries <-chan *metadata.PackageMetadata) (int, error) {
	return IngestWithProgress(db, entries, nil)
}

// IngestWithProgress is like Ingest and reports the committed entry count
// after each batch. The callback runs synchronously and should return quickly.
func IngestWithProgress(db *badger.DB, entries <-chan *metadata.PackageMetadata, progress func(int)) (int, error) {
	count := 0
	batchCount := 0
	wb := db.NewWriteBatch()
	defer wb.Cancel()

	for entry := range entries {
		if entry == nil {
			continue
		}

		var buf bytes.Buffer
		enc := gob.NewEncoder(&buf)
		if err := enc.Encode(entry); err != nil {
			return count, fmt.Errorf("ingest: encode %s: %w", entry.Key(), err)
		}

		key := encodeRecordKey(entry)
		val := buf.Bytes()

		if len(val) > 64<<20 {
			continue
		}

		if err := wb.Set(key, val); err != nil {
			return count, fmt.Errorf("ingest: write batch set %s (%d bytes): %w", entry.Key(), len(val), err)
		}
		if err := setSecondaryIndexes(wb, entry); err != nil {
			return count, err
		}

		batchCount++
		count++

		if batchCount >= maxBatchSize {
			if err := wb.Flush(); err != nil {
				return count, fmt.Errorf("ingest: flush: %w", err)
			}
			wb.Cancel()
			wb = db.NewWriteBatch()
			batchCount = 0
			if progress != nil {
				progress(count)
			}
		}
	}

	if batchCount > 0 {
		if err := wb.Flush(); err != nil {
			return count, fmt.Errorf("ingest: final flush: %w", err)
		}
	}
	if progress != nil {
		progress(count)
	}

	return count, nil
}

// ReconcileWithProgress incrementally updates changed package records and
// returns the set of keys observed in the source snapshot. Call RemoveMissing
// only when the source walk completed without errors.
func ReconcileWithProgress(db *badger.DB, entries <-chan *metadata.PackageMetadata, progress func(int)) (ReconcileStats, map[string]struct{}, error) {
	stats := ReconcileStats{}
	seen := make(map[string]struct{})
	selected := make(map[string]*metadata.PackageMetadata)
	for entry := range entries {
		if entry == nil {
			continue
		}
		stats.Seen++
		key := entry.RepositoryCPVKey()
		seen[key] = struct{}{}
		if current := selected[key]; current == nil || preferMetadataFingerprint(entry, current) {
			selected[key] = entry
		}
		if progress != nil {
			progress(stats.Seen)
		}
	}
	existingFingerprints, err := loadFingerprints(db)
	if err != nil {
		return stats, seen, fmt.Errorf("load fingerprint index: %w", err)
	}
	wb := db.NewWriteBatch()
	defer wb.Cancel()
	batchCount := 0

	flush := func() error {
		if batchCount == 0 {
			return nil
		}
		if err := wb.Flush(); err != nil {
			return err
		}
		wb.Cancel()
		wb = db.NewWriteBatch()
		batchCount = 0
		return nil
	}

	for key, entry := range selected {
		fingerprint, err := metadata.Fingerprint(entry)
		if err != nil {
			return stats, seen, fmt.Errorf("fingerprint %s: %w", key, err)
		}
		existingFingerprint := existingFingerprints[key]
		if existingFingerprint == fingerprint {
			stats.Unchanged++
		} else {
			previous, err := queryIdentity(db, key)
			if err != nil {
				return stats, seen, fmt.Errorf("read previous %s: %w", key, err)
			}
			var buf bytes.Buffer
			if err := gob.NewEncoder(&buf).Encode(entry); err != nil {
				return stats, seen, fmt.Errorf("encode %s: %w", key, err)
			}
			if buf.Len() > 64<<20 {
				continue
			}
			if err := wb.Set(encodeIdentityKey(key), buf.Bytes()); err != nil {
				return stats, seen, fmt.Errorf("write batch set %s: %w", key, err)
			}
			if err := wb.Set(encodeFingerprintKey(key), fingerprint[:]); err != nil {
				return stats, seen, fmt.Errorf("write fingerprint %s: %w", key, err)
			}
			if err := deleteSecondaryIndexes(wb, previous); err != nil {
				return stats, seen, err
			}
			if err := setSecondaryIndexes(wb, entry); err != nil {
				return stats, seen, err
			}
			batchCount++
			stats.Changed++
			if batchCount >= maxBatchSize {
				if err := flush(); err != nil {
					return stats, seen, fmt.Errorf("flush incremental batch: %w", err)
				}
			}
		}
	}
	if err := flush(); err != nil {
		return stats, seen, fmt.Errorf("final incremental flush: %w", err)
	}
	return stats, seen, nil
}

func preferMetadata(candidate, current *metadata.PackageMetadata) bool {
	candidateVersion, _ := atom.ParseVersion(candidate.Version)
	currentVersion, _ := atom.ParseVersion(current.Version)
	if candidateVersion == nil {
		if currentVersion == nil {
			if candidate.Version != current.Version {
				return candidate.Version > current.Version
			}
			return preferMetadataFingerprint(candidate, current)
		}
		return false
	}
	if currentVersion == nil {
		return true
	}
	if comparison := candidateVersion.Compare(currentVersion); comparison != 0 {
		return comparison > 0
	}
	if candidate.OverlayIndex != current.OverlayIndex {
		return candidate.OverlayIndex > current.OverlayIndex
	}
	return preferMetadataFingerprint(candidate, current)
}

func preferMetadataFingerprint(candidate, current *metadata.PackageMetadata) bool {
	candidateFingerprint, candidateErr := metadata.Fingerprint(candidate)
	currentFingerprint, currentErr := metadata.Fingerprint(current)
	if candidateErr != nil || currentErr != nil {
		return false
	}
	return bytes.Compare(candidateFingerprint[:], currentFingerprint[:]) > 0
}

// RemoveMissing deletes indexed packages absent from a successfully completed
// source walk.
func RemoveMissing(db *badger.DB, seen map[string]struct{}) (int, error) {
	type staleRecord struct {
		key      []byte
		identity string
		metadata *metadata.PackageMetadata
	}
	var stale []staleRecord
	err := db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(keyPrefix)
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(opts.Prefix); it.ValidForPrefix(opts.Prefix); it.Next() {
			key := it.Item().KeyCopy(nil)
			identity := strings.TrimPrefix(string(key), keyPrefix)
			if _, ok := seen[identity]; !ok {
				var m *metadata.PackageMetadata
				if err := it.Item().Value(func(value []byte) error {
					var decodeErr error
					m, decodeErr = decodeValue(value)
					return decodeErr
				}); err != nil {
					return err
				}
				stale = append(stale, staleRecord{key: key, identity: identity, metadata: m})
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("scan stale packages: %w", err)
	}
	wb := db.NewWriteBatch()
	defer wb.Cancel()
	for _, record := range stale {
		if err := wb.Delete(record.key); err != nil {
			return 0, fmt.Errorf("delete stale package: %w", err)
		}
		if err := wb.Delete(encodeFingerprintKey(record.identity)); err != nil {
			return 0, fmt.Errorf("delete stale fingerprint: %w", err)
		}
		if err := deleteSecondaryIndexes(wb, record.metadata); err != nil {
			return 0, err
		}
	}
	if len(stale) > 0 {
		if err := wb.Flush(); err != nil {
			return 0, fmt.Errorf("flush stale packages: %w", err)
		}
	}
	return len(stale), nil
}

// Query retrieves the preferred PackageMetadata for a category/package key.
// All versions remain stored; this compatibility API selects by repository
// priority and then Gentoo version ordering.
func Query(db *badger.DB, cp string) (*metadata.PackageMetadata, error) {
	var m *metadata.PackageMetadata

	err := db.View(func(txn *badger.Txn) error {
		prefix := append(encodeKey(cp), 0)
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			if err := it.Item().Value(func(val []byte) error {
				candidate, err := decodeValue(val)
				if err == nil && (m == nil || preferMetadata(candidate, m)) {
					m = candidate
				}
				return err
			}); err != nil {
				return err
			}
		}
		// Schema-1 databases stored one selected record directly at pkg:<CP>.
		// Keep queries usable until the next index pass migrates the snapshot.
		if m == nil {
			if item, err := txn.Get(encodeKey(cp)); err == nil {
				return item.Value(func(val []byte) error {
					var decodeErr error
					m, decodeErr = decodeValue(val)
					return decodeErr
				})
			} else if err != badger.ErrKeyNotFound {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("ingest: query: %w", err)
	}

	return m, nil
}

// QueryVersions returns every repository CPV record for cp. Records are kept
// distinct so visibility and repository-priority evaluation can be performed
// without reconstructing information discarded at index time.
func QueryVersions(db *badger.DB, cp string) ([]*metadata.PackageMetadata, error) {
	var records []*metadata.PackageMetadata
	err := db.View(func(txn *badger.Txn) error {
		prefix := append(encodeKey(cp), 0)
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			if err := it.Item().Value(func(val []byte) error {
				m, err := decodeValue(val)
				if err == nil {
					records = append(records, m)
				}
				return err
			}); err != nil {
				return err
			}
		}
		if len(records) == 0 {
			if item, err := txn.Get(encodeKey(cp)); err == nil {
				return item.Value(func(val []byte) error {
					m, decodeErr := decodeValue(val)
					if decodeErr == nil {
						records = append(records, m)
					}
					return decodeErr
				})
			} else if err != badger.ErrKeyNotFound {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("ingest: query versions: %w", err)
	}
	return records, nil
}

// QuerySlot returns repository CPV records indexed under an exact SLOT.
func QuerySlot(db *badger.DB, slot string) ([]*metadata.PackageMetadata, error) {
	return querySecondary(db, slotIndexPrefix, slot)
}

// QueryRepository returns every CPV record belonging to a repository name.
func QueryRepository(db *badger.DB, repository string) ([]*metadata.PackageMetadata, error) {
	return querySecondary(db, repoIndexPrefix, repository)
}

func QueryKeyword(db *badger.DB, keyword string) ([]*metadata.PackageMetadata, error) {
	return querySecondary(db, keywordIndexPrefix, keyword)
}

func QueryLicense(db *badger.DB, license string) ([]*metadata.PackageMetadata, error) {
	return querySecondary(db, licenseIndexPrefix, license)
}

func QueryEAPI(db *badger.DB, eapi string) ([]*metadata.PackageMetadata, error) {
	return querySecondary(db, eapiIndexPrefix, eapi)
}

// QueryRange iterates over all entries whose key matches the given prefix.
// The callback fn is called for each decoded entry. Iteration stops early if
// fn returns io.EOF.
func QueryRange(db *badger.DB, prefix string, fn func(*metadata.PackageMetadata) error) error {
	return db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefix)

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek([]byte(prefix)); it.ValidForPrefix([]byte(prefix)); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				m, decodeErr := decodeValue(val)
				if decodeErr != nil {
					return decodeErr
				}
				return fn(m)
			})
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
		}
		return nil
	})
}

// QueryRangeParallel retains database iterator order while decoding values in
// parallel. Repository metadata decoding is CPU-heavy and independent per CPV;
// callbacks remain serial and deterministic.
func QueryRangeParallel(db *badger.DB, prefix string, workers int, fn func(*metadata.PackageMetadata) error) error {
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	if workers < 2 {
		return QueryRange(db, prefix, fn)
	}
	var encoded [][]byte
	if err := db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefix)
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek([]byte(prefix)); it.ValidForPrefix([]byte(prefix)); it.Next() {
			value, err := it.Item().ValueCopy(nil)
			if err != nil {
				return err
			}
			encoded = append(encoded, value)
		}
		return nil
	}); err != nil {
		return err
	}

	decoded := make([]*metadata.PackageMetadata, len(encoded))
	jobs := make(chan int, workers*2)
	var wait sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				value, err := decodeValue(encoded[index])
				if err != nil {
					errOnce.Do(func() { firstErr = err })
					continue
				}
				decoded[index] = value
			}
		}()
	}
	for index := range encoded {
		jobs <- index
	}
	close(jobs)
	wait.Wait()
	if firstErr != nil {
		return firstErr
	}
	for _, value := range decoded {
		if err := fn(value); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
	return nil
}

// QueryKeys iterates over package keys without loading or decoding their values.
// The callback receives canonical category/package strings.
func QueryKeys(db *badger.DB, prefix string, fn func(string) error) error {
	return db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefix)
		opts.PrefetchValues = false

		it := txn.NewIterator(opts)
		defer it.Close()
		last := ""
		for it.Seek([]byte(prefix)); it.ValidForPrefix([]byte(prefix)); it.Next() {
			identity := strings.TrimPrefix(string(it.Item().Key()), keyPrefix)
			cp := strings.SplitN(identity, "\x00", 2)[0]
			if cp == last {
				continue
			}
			last = cp
			if err := fn(cp); err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
		}
		return nil
	})
}

// CountRecords validates and counts primary package keys without decoding
// values. Badger verifies table integrity while opening the candidate; every
// value was already encoded during reconciliation.
func CountRecords(db *badger.DB) (int, error) {
	count := 0
	err := db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(keyPrefix)
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(opts.Prefix); it.ValidForPrefix(opts.Prefix); it.Next() {
			count++
		}
		return nil
	})
	return count, err
}

func encodeKey(cp string) []byte {
	return []byte(keyPrefix + cp)
}

func encodeRecordKey(m *metadata.PackageMetadata) []byte {
	return encodeIdentityKey(m.RepositoryCPVKey())
}

func encodeIdentityKey(identity string) []byte {
	return []byte(keyPrefix + identity)
}

func encodeFingerprintKey(cp string) []byte {
	return []byte(fingerprintPrefix + cp)
}

func secondaryKeys(m *metadata.PackageMetadata) [][]byte {
	identity := m.RepositoryCPVKey()
	keys := [][]byte{[]byte(cpIndexPrefix + m.Key() + "\x00" + identity)}
	if m.SLOT != "" {
		keys = append(keys, []byte(slotIndexPrefix+m.SLOT+"\x00"+identity))
	}
	if m.Repository != "" {
		keys = append(keys, []byte(repoIndexPrefix+m.Repository+"\x00"+identity))
	}
	for _, keyword := range strings.Fields(m.KEYWORDS) {
		keys = append(keys, []byte(keywordIndexPrefix+keyword+"\x00"+identity))
	}
	for _, license := range strings.Fields(m.LICENSE) {
		keys = append(keys, []byte(licenseIndexPrefix+license+"\x00"+identity))
	}
	if m.EAPI != "" {
		keys = append(keys, []byte(eapiIndexPrefix+m.EAPI+"\x00"+identity))
	}
	return keys
}

func setSecondaryIndexes(wb *badger.WriteBatch, m *metadata.PackageMetadata) error {
	primary := encodeRecordKey(m)
	for _, key := range secondaryKeys(m) {
		if err := wb.Set(key, primary); err != nil {
			return fmt.Errorf("write secondary index %q: %w", key, err)
		}
	}
	return nil
}

func deleteSecondaryIndexes(wb *badger.WriteBatch, m *metadata.PackageMetadata) error {
	if m == nil {
		return nil
	}
	for _, key := range secondaryKeys(m) {
		if err := wb.Delete(key); err != nil {
			return fmt.Errorf("delete secondary index %q: %w", key, err)
		}
	}
	return nil
}

func querySecondary(db *badger.DB, indexPrefix, value string) ([]*metadata.PackageMetadata, error) {
	var records []*metadata.PackageMetadata
	err := db.View(func(txn *badger.Txn) error {
		prefix := []byte(indexPrefix + value + "\x00")
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			if err := it.Item().Value(func(primary []byte) error {
				item, err := txn.Get(primary)
				if err != nil {
					return err
				}
				return item.Value(func(encoded []byte) error {
					m, err := decodeValue(encoded)
					if err == nil {
						records = append(records, m)
					}
					return err
				})
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("ingest: query secondary index: %w", err)
	}
	return records, nil
}

func queryIdentity(db *badger.DB, identity string) (*metadata.PackageMetadata, error) {
	var m *metadata.PackageMetadata
	err := db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(encodeIdentityKey(identity))
		if err == badger.ErrKeyNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		return item.Value(func(value []byte) error {
			var decodeErr error
			m, decodeErr = decodeValue(value)
			return decodeErr
		})
	})
	return m, err
}

func loadFingerprints(db *badger.DB) (map[string][32]byte, error) {
	fingerprints := make(map[string][32]byte)
	err := db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(fingerprintPrefix)
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(opts.Prefix); it.ValidForPrefix(opts.Prefix); it.Next() {
			cp := strings.TrimPrefix(string(it.Item().Key()), fingerprintPrefix)
			if err := it.Item().Value(func(value []byte) error {
				if len(value) != 32 {
					return fmt.Errorf("invalid fingerprint length %d for %s", len(value), cp)
				}
				var fingerprint [32]byte
				copy(fingerprint[:], value)
				fingerprints[cp] = fingerprint
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return fingerprints, err
}

func decodeValue(val []byte) (*metadata.PackageMetadata, error) {
	buf := bytes.NewBuffer(val)
	dec := gob.NewDecoder(buf)
	var m metadata.PackageMetadata
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &m, nil
}

func DecodeEntry(val []byte) (*metadata.PackageMetadata, error) {
	return decodeValue(val)
}
